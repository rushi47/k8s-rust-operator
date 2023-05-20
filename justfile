#default recipe
default:
  just --list

_default := ""
_default_all := "--all"

#format all the rust code
fmt:
   gofmt *

#Will run the controller
run:
   go run *.go

# This will not work as we need to create multicluster, sticking to create script for now. 
export K3D_ORG_DOMAIN := env_var_or_default("K3D_ORG_DOMAIN", "cluster.local")
export K3D_NETWORK_NAME := env_var_or_default("K3D_NETWORK_NAME", "svc-mirror-network")
export K3D_CLUSTER_NAME := env_var_or_default("K3D_CLUSTER_NAME", "svc-mirror-mc")

#Spins up 3 k3d cluster
k3d-create: && install-linkerd
   #!/usr/bin/env bash
   set -euxo pipefail
   port=6440
   for cluster in source target1 target2 ; do
      if k3d cluster get "$cluster" >/dev/null 2>&1 ; then
         echo "Already exists: $cluster" >&2
         ((port++))
      else
         k3d cluster create "$cluster" \
            --network='{{ K3D_NETWORK_NAME }}' \
            --api-port="$((port++))" \
            --k3s-arg="--cluster-domain=$cluster.{{ K3D_ORG_DOMAIN }}@server:0" \
            --kubeconfig-update-default \
            --kubeconfig-switch-context=false
      fi
   done


#Install linkerd 
install-linkerd: && link-mc
   #!/usr/bin/env bash
   set -euxo pipefail

   ORG_DOMAIN="${ORG_DOMAIN:-cluster.local}"
   LINKERD="${LINKERD:-linkerd}"

   case $(uname) in
      Darwin)
         # host_platform=darwin
         CA_DIR=$(mktemp -d ./bootstrap-scripts/k3d-ca.XXXXX)
         ;;
      Linux)
         # host_platform=linux
         CA_DIR=$(mktemp --tmpdir="${TMPDIR:-./bootstrap-scripts}" -d k3d-ca.XXXXX)
         ;;
      *)
         echo "Unknown operating system: $(uname)"
         exit 1
         ;;
   esac

   # Generate the trust roots. These never touch the cluster. In the real world
   # wed squirrel these away in a vault.
   step certificate create \
      "identity.linkerd.${ORG_DOMAIN}" \
      "$CA_DIR/ca.crt" "$CA_DIR/ca.key" \
      --profile root-ca \
      --no-password  --insecure --force

   for cluster in source target1 target2; do
      # Check that the cluster is up and running.
      while ! $LINKERD --context="k3d-$cluster" check --pre ; do :; done

      # Create issuing credentials. These end up on the cluster (and can be
      # rotated from the root).
      crt="${CA_DIR}/${cluster}-issuer.crt"
      key="${CA_DIR}/${cluster}-issuer.key"
      domain="${cluster}.${ORG_DOMAIN}"
      step certificate create "identity.linkerd.${domain}" \
         "$crt" "$key" \
         --ca="$CA_DIR/ca.crt" \
         --ca-key="$CA_DIR/ca.key" \
         --profile=intermediate-ca \
         --not-after 8760h --no-password --insecure

      #Install CRDs into cluster
      $LINKERD --context="k3d-$cluster" install --crds |  kubectl --context="k3d-$cluster" apply -f -

      # Install Linkerd into the cluster.
      $LINKERD --context="k3d-$cluster" install \
               --proxy-log-level="linkerd=debug,warn" \
               --cluster-domain="$domain" \
               --identity-trust-domain="$domain" \
               --identity-trust-anchors-file="$CA_DIR/ca.crt" \
               --identity-issuer-certificate-file="${crt}" \
               --identity-issuer-key-file="${key}" |
         kubectl --context="k3d-$cluster" apply -f -

      # Wait some time and check that the cluster has started properly.
      sleep 30
      while ! $LINKERD --context="k3d-$cluster" check ; do :; done

      kubectl --context="k3d-$cluster" create ns linkerd-multicluster
      sleep 2

      # Setup the multicluster components on the server
      $LINKERD --context="k3d-$cluster" multicluster install |
         kubectl --context="k3d-$cluster" apply -f -
   done

#Link multilcusters
link-mc: && install-testset
   #!/usr/bin/env bash
   set -euxo pipefail
   
   ORG_DOMAIN="${ORG_DOMAIN:-cluster.local}"
   LINKERD="${LINKERD:-linkerd}"
   
   fetch_credentials() {
      cluster="$1"
      # Grab the LB IP of clusters API server & replace it in the secret blob:
      lb_ip=$(kubectl --context="$cluster" get svc -n kube-system traefik -o json | jq -r '.status.loadBalancer.ingress[0].ip')
      
      # shellcheck disable=SC2001  
      echo "$($LINKERD --context="$cluster" \
               multicluster link --set "enableHeadlessServices=true" \
               --cluster-name="$cluster" \
               --log-level="debug" \
               --api-server-address="https://${lb_ip}:6443")" 
   }

   # source, target1 & target2 get access to each other.
   fetch_credentials k3d-source | kubectl --context=k3d-target1 apply -n linkerd-multicluster -f -
   fetch_credentials k3d-target2 | kubectl --context=k3d-target1 apply -n linkerd-multicluster -f -

   fetch_credentials k3d-target1 | kubectl --context=k3d-source apply -n linkerd-multicluster -f -
   fetch_credentials k3d-target2 | kubectl --context=k3d-source apply -n linkerd-multicluster -f -

   fetch_credentials k3d-target1 | kubectl --context=k3d-target2 apply -n linkerd-multicluster -f -
   fetch_credentials k3d-source | kubectl --context=k3d-target2 apply -n linkerd-multicluster -f -

   sleep 10
   for c in k3d-source k3d-target1 ; do
      $LINKERD --context="$c" mc check
   done

#Install 2 statefulset in target1 & target2 multicluster & dnsutil for testing in source
install-testset: && set-context
   kubectl kustomize bootstrap-scripts/  | kubectl --context=k3d-target1 apply -f -
   kubectl kustomize bootstrap-scripts/  | kubectl --context=k3d-target2 apply -f -

   cat bootstrap-scripts/dns-utils.yaml | kubectl --context=k3d-source apply -f -

#Delete testset
delete-testset:
   kubectl kustomize bootstrap-scripts/  | kubectl --context=k3d-target1 delete -f -
   kubectl kustomize bootstrap-scripts/  | kubectl --context=k3d-target2 delete -f -

   cat bootstrap-scripts/dns-utils.yaml | kubectl --context=k3d-source delete -f -

#Set kubectl context to source
set-context:
   kubectl config use-context k3d-source

#Delete all three clusters.
k3d-delete:
   #!/usr/bin/env bash
   set -euxo pipefail
   for cluster in source target1 target2 ; do
      k3d cluster delete "$cluster";
   done
