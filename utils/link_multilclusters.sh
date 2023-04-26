#!/bin/sh

set -eu

ORG_DOMAIN="${ORG_DOMAIN:-cluster.local}"
LINKERD="${LINKERD:-linkerd}"


# Generate credentials so the service-mirror
#
# Unfortunately, the credentials have the API server IP as addressed from
# localhost and not the docker network, so we have to patch that up.
fetch_credentials() {
    cluster="$1"
    # Grab the LB IP of cluster's API server & replace it in the secret blob:
    lb_ip=$(kubectl --context="$cluster" get svc -n kube-system traefik \
	    -o 'go-template={{ (index .status.loadBalancer.ingress 0).ip }}')
    
    # shellcheck disable=SC2001  
    echo "$($LINKERD --context="$cluster" \
            multicluster link --set "enableHeadlessServices=true" \
            --cluster-name="$cluster" \
            --log-level="debug" \
            --api-server-address="https://${lb_ip}:6443")" 
}

# East & West get access to each other.
fetch_credentials k3d-east | kubectl --context=k3d-west apply -n linkerd-multicluster -f -

fetch_credentials k3d-west | kubectl --context=k3d-east apply -n linkerd-multicluster -f -

sleep 10
for c in k3d-east k3d-west ; do
    $LINKERD --context="$c" mc check
done
