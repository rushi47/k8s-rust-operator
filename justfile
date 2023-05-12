#default recipe
default:
  just --list

_default := ""
_default_all := "--all"

#format all the rust code
fmt:
   {{ _cargo }} fmt {{ _default_all }}

#lint all the code, takes --all as param value for fix, default to doesnt fix
lint clipy_args=_default:
   {{ _cargo }} clipy {{ clipy_args }}

#create k3d cluster east and west
create_k3d_cluster: && install_linkerd
   cd utils && ./create_k3d.sh

#install linkerd and install multicluster extension
install_linkerd: && link_clusters
   cd utils &&  ./install_linkerd.sh

#link created two multiclusters
link_clusters:
   cd utils && ./link_multilclusters.sh

export K3D_ORG_DOMAIN = env_var_or_default("K3D_ORG_DOMAIN", "cluster.local")
export K3D_NETWORK_NAME = env_var_or_default("K3D_NETWORK_NAME")
export K3D_CLUSTER_NAME = env_var_or_default("K3D_CLUSTER_NAME")
k3d-create:
  k3d cluster create {{ K3D_CLUSTER_NAME }} \
        --network='{{ K3D_NETWORK_NAME }}' \
        --k3s-arg="--cluster-domain={{ K3D_ORG_DOMAIN }}@server:0"
        {{ _k3d-flags }} \
        --kubeconfig-update-default \
        --kubeconfig-switch-context=false
