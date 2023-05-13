use anyhow::Context as any_context;
use anyhow::Result;
use k8s_openapi::api::discovery::v1::EndpointPort;
use k8s_openapi::api::{
    core::v1::Service,
    discovery::v1::{Endpoint, EndpointSlice},
};
use kube::api::ListParams;
use kube::api::PostParams;
use kube::core::ObjectList;
use kube::{core::ObjectMeta, Api, ResourceExt};
use std::{collections::BTreeMap, sync::Arc};

use super::super::Context;

fn get_eps_labels(svc_refer_to: String, cluster_name: String) -> BTreeMap<String, String> {
    let mut lables: BTreeMap<String, String> = BTreeMap::new();
    //Label to keep backref of orginal cluster
    lables.insert("mirror.linkerd.io/cluster-name".to_string(), cluster_name);
    //Service name of global service, and managed by global mirror
    lables.insert("kubernetes.io/service-name".to_string(), svc_refer_to);
    lables.insert(
        "kubernetes.io/managed-by".to_string(),
        "global-mirror-controller".to_string(),
    );

    lables
}

pub async fn get_endpoint_slice(
    ctx: Arc<Context>,
    svc_name: String,
) -> Result<ObjectList<EndpointSlice>> {
    /*
       - Query endpointslice on the basis of label: kubernetes.io/service-name
    */
    let ep_slices: Api<EndpointSlice> = Api::all(ctx.0.clone());
    let ep_slice_name = format!("kubernetes.io/service-name={svc_name}");
    let ep_filter = ListParams::default().labels(&ep_slice_name);

    let endpoints_list = ep_slices.list(&ep_filter).await.with_context(|| {
        format!(
            "Unable to list endpoint slices for filter : {:?}",
            ep_filter
        )
    })?;
    Ok(endpoints_list)
}

pub fn list_endpoints(
    _ctx: Arc<Context>,
    endpoints_list: &ObjectList<EndpointSlice>,
    cluster_name: String,
) -> Vec<Endpoint> {
    /*
        - Get all the subset of addresses for this service.
        - Modify hostname to include cluster name at the end
    */
    let mut ep_address: Vec<Endpoint> = Vec::new();
    for eps in endpoints_list.iter() {
        for ep in eps.endpoints.iter() {
            // println!("Endpoint for the Endpoint Slice : {:?} ", ep);
            //Add cluster name at the end
            let hostname = match ep.hostname.clone() {
                Some(hostname) => {
                    let mut hostname_w_cluster = hostname.clone();
                    if !(hostname.contains(&cluster_name)) {
                        hostname_w_cluster = format!("{}-{}", hostname, cluster_name);
                    }
                    hostname_w_cluster
                }
                _ => String::new(),
            };

            let mut tmp_ep = ep.clone();
            tmp_ep.hostname = Some(hostname);
            ep_address.push(tmp_ep);
        }
    }
    ep_address
}

pub fn list_ports(
    _ctx: Arc<Context>,
    endpoints_list: &ObjectList<EndpointSlice>,
) -> Vec<EndpointPort> {
    // Get existing ports from EndpointSlice.
    let mut ep_ports: Vec<EndpointPort> = Vec::new();

    for eps in endpoints_list.iter() {
        for ports in eps.ports.iter() {
            for port in ports.iter() {
                ep_ports.push(port.clone());
            }
        }
    }

    ep_ports
}

fn get_address_type(eps_list: &ObjectList<EndpointSlice>) -> String {
    for ep in eps_list {
        return ep.address_type.clone();
    }
    //Default to IPV4
    "IPv4".to_string()
}

pub async fn create_ep_slice(
    ctx: Arc<Context>,
    headless_svc: &Arc<Service>,
    global_svc_name: String,
    cluster_name: String,
) -> Result<EndpointSlice> {
    /*

    - Create EndpointSlice for each target cluster
        - Name for the EndpointSlice will be : X-global-target-cluster-name
        - Endpoint Address will have  addresses retrieved from headless svc EndpointSlice
        with suffix of their cluster. Ex. nginx-set-1-k3d-west, nginx-set-2-k3d-east
        - This will create A record with hostname.global-svc-name and will help to query individual endpoint.
    - Multiple EndpointSlice will back the service : X-global using label : kubernetes.io/service-name.
    */

    //Get namespace
    let ns = match headless_svc.namespace() {
        Some(ns) => ns,
        _ => String::new(),
    };

    //Get EndpointSlice for target service
    let hdls_eps = get_endpoint_slice(ctx.clone(), headless_svc.name_any()).await?;

    //Get existing Endpoints from EndpointSlice of target
    let target_endpoints: Vec<Endpoint> =
        list_endpoints(ctx.clone(), &hdls_eps, cluster_name.clone());

    //Get existing ports from EndpointSlice
    //TO DO: make sure below ports exists in the global service.
    let target_ports: Vec<EndpointPort> = list_ports(ctx.clone(), &hdls_eps);

    //Get address type
    let address_type = get_address_type(&hdls_eps);

    // Name of the endpoint slice will form by suffixing global service name with target cluster.
    let eps_name = format!("{}-{}", global_svc_name, cluster_name);

    let endpointslice_meta = ObjectMeta {
        name: Some(eps_name.clone()),
        namespace: Some(ns.clone()),
        labels: Some(get_eps_labels(global_svc_name, cluster_name)),
        ..Default::default()
    };

    let endpointslice = EndpointSlice {
        metadata: endpointslice_meta,
        endpoints: target_endpoints,
        ports: Some(target_ports),
        address_type: address_type,
        ..Default::default()
    };

    let api_ep_slice: Api<EndpointSlice> = Api::namespaced(ctx.0.clone(), &ns);

    api_ep_slice
        .create(&PostParams::default(), &endpointslice)
        .await
        .with_context(|| format!("Unable to create EndpointSlice with name : {}", eps_name))?;
    Ok(endpointslice)
}
