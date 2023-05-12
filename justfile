#default recipe
default:
  just --list

_default := ""
_default_all := "--all"

#format all the rust code
fmt:
   cargo fmt {{ _default_all }}

#lint all the code, takes --all as param value for fix, default to doesnt fix
lint clipy_args=_default:
   cargo clipy {{ clipy_args }}

#create multi k3d cluster east and west
create_k3d_cluster: && install_linkerd
   cd utils && rm -rf k3d-ca* && ./create_k3d.sh

#install linkerd and install multicluster extension
install_linkerd: && link_clusters
   cd utils &&  ./install_linkerd.sh

#link created two multiclusters
link_clusters:
   cd utils && ./link_multilclusters.sh

#Delete one or more clusters 
delete_k3d +clusternames:
   k3d cluster delete {{ clusternames }}

# This will not work as we need to create multicluster, sticking to create script for now. 
# TO DO : Make it work for multi cluster
export K3D_ORG_DOMAIN := env_var_or_default("K3D_ORG_DOMAIN", "cluster.local")
export K3D_NETWORK_NAME := env_var_or_default("K3D_NETWORK_NAME", "svc-mirror-network")
export K3D_CLUSTER_NAME := env_var_or_default("K3D_CLUSTER_NAME", "svc-mirror-mc")

k3d-create:
  k3d cluster create {{ K3D_CLUSTER_NAME }} \
        --network='{{ K3D_NETWORK_NAME }}' \
        --k3s-arg="--cluster-domain={{ K3D_ORG_DOMAIN }}@server:0" \
        --kubeconfig-update-default \
        --kubeconfig-switch-context=false
