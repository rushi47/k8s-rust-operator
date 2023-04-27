#Rust fmt and linter together
fl: fmt lint
setup_cluster: create_k3d_cluster

_cargo := "cargo"
_default := ""
_default_all := "--all"

#format all the rust code
fmt:
   {{ _cargo }} fmt {{ _default_all }}

#lint all the code, takes --all as param value for fix, default to doesnt fix
lint clipy_args=_default:
   {{ _cargo }} clipy {{ clipy_args }}

#run controller
run:
   {{ _cargo }} run

#create k3d cluster east and west
create_k3d_cluster: && install_linkerd
   cd utils && ./create_k3d.sh

#install linkerd and install multicluster extension
install_linkerd: && link_clusters
   cd utils &&  ./install_linkerd.sh

#link created two multiclusters
link_clusters:
   cd utils && ./link_multilclusters.sh