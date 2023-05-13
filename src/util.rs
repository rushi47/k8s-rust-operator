use k8s_openapi::api::core::v1::{EndpointAddress, Endpoints, Service, ServicePort};
use kube::api::{Api, ListParams, ResourceExt};
use std::sync::Arc;

use ::anyhow::Result;
use anyhow::Context as anycontext;

use super::Context;

pub async fn get_parent_name(svc: Arc<Service>) -> Result<String> {
    // Get the name of headless service.
    let label_key_svc_name = "mirror.linkerd.io/headless-mirror-svc-name".to_string();

    let mut parent_svc_name = String::new();

    // Check if the key exist inside labels
    if svc.labels().contains_key(&label_key_svc_name) {
        parent_svc_name = match svc.labels().get(&label_key_svc_name) {
            Some(svc_name) => svc_name.clone(),
            _ => svc.name_any(),
        }
    } else {
        // If key doesnt exist inside label, that means name of the service is the current service
        parent_svc_name = svc.name_any();
    }

    Ok(parent_svc_name)
}

//Check if the global service exists
pub async fn check_if_aggregation_service_exists(
    svc: Arc<Service>,
    ctx: Arc<Context>,
) -> Result<(bool, String)> {
    /*
        - Check if the label has key : mirror.linkerd.io/headless-mirror-svc-name
            - if yes it means that this is child service of headless service - mirror.linkerd.io/headless-mirror-svc-name
            else it means that this service is the headless service and it has childs

        - If global service doesnt exist, remove the cluster name from value of label:  mirror.linkerd.io/headless-mirror-svc-name
        and then suffix with `-global`. Make sure there should be only one service.
    */

    let parent_svc_name = get_parent_name(svc.clone())
        .await
        .with_context(|| "Unable to get parent name")?;

    //Get the cluster name,
    let label_key_cluster_name = "mirror.linkerd.io/cluster-name".to_string();
    let mut target_cluster_name = String::new();

    // Check if the key exist inside labels
    if svc.labels().contains_key(&label_key_cluster_name) {
        match svc.labels().get(&label_key_cluster_name) {
            Some(target_name) => target_cluster_name = target_name.to_string(),
            _ => println!(
                "Unable to get the name of target cluster : {}",
                label_key_cluster_name
            ),
        }
    }

    //Remove target cluster name from the headless service and create global service name.
    target_cluster_name = format!("-{target_cluster_name}");

    let global_svc_name = parent_svc_name.replace(&target_cluster_name, "-global");

    println!("Checking if the global service named : {global_svc_name} exists");

    let svc: Api<Service> = Api::all(ctx.0.clone());

    let label_filter = format!("metadata.name={global_svc_name}");

    let svc_filter = ListParams::default().fields(&label_filter);

    //check if any service with global name exist
    let svc_received = svc.list(&svc_filter).await.with_context(|| {
        format!(
            "Unable to list services to check if the aggregation service exist for filter : {:?}",
            svc_filter
        )
    })?;

    //Global service exist by the name
    if svc_received.items.len() > 0 {
        return Ok((true, global_svc_name));
    }

    Ok((false, global_svc_name))
}

pub async fn list_svc_port(svc: Arc<Service>, ctx: Arc<Context>) -> Result<Vec<ServicePort>> {
    /*
        -  Get the ports for mirrored Headless Service.

        Ex. If below is the state after mirroring the service with 2 statefulsets. In this case, nginx-svc-k3d-west
        is the headless service which referes to nginx-set-0-k3d-west, nginx-set-1-k3d-west
        $kubectl get svc
        NAME                              TYPE        CLUSTER-IP     EXTERNAL-IP   PORT(S)   AGE
        nginx-svc-k3d-west                ClusterIP   None           <none>        80/TCP    14h
        nginx-set-0-k3d-west              ClusterIP   10.43.32.30    <none>        80/TCP    19m
        nginx-set-1-k3d-west              ClusterIP   10.43.25.117   <none>        80/TCP    19m

        Even if the reconciler is getting reconciled for nginx-set-0-k3d-west, we will check the ports for :
        nginx-svc-k3d-west. As this is the main headless service and has all the information required.
        To figure out,  nginx-set-0-k3d-west is reffering to which headless svc ? We will make use of label :
        `mirror.linkerd.io/headless-mirror-svc-name` placed on nginx-set-0-k3d-west.
    */

    println!("Listing the service port for : {}", svc.name_any());

    let services: Api<Service> = Api::all(ctx.0.clone());

    let svc_name = format!("metadata.name={}", svc.name_any());

    let svc_filter = ListParams::default().fields(&svc_name);

    let mut service_ports: Vec<ServicePort> = Vec::new();

    let services_list = services
        .list(&svc_filter)
        .await
        .with_context(|| format!("Unable to list services for filter {:?}", svc_filter))?;

    for svc in services_list {
        for svc_spec in svc.spec.iter() {
            let vec_port = svc_spec.ports.clone();
            match vec_port {
                Some(ports) => {
                    //Check if already the same object exist (not full fledge solution to check dedup)
                    for svc_port in ports.iter() {
                        if !service_ports.contains(svc_port) {
                            service_ports.push(svc_port.clone());
                        }
                    }
                }
                _ => (),
            }
        }
    }

    Ok(service_ports)
}

pub async fn list_endpoints(svc: String, ctx: Arc<Context>) -> Result<Vec<EndpointAddress>> {
    /*
        - Query the Endpoint for headless service of target cluster.
        - Get all the subset of addresses for this service.
    */
    let ep_slices: Api<Endpoints> = Api::all(ctx.0.clone());

    let mut ep_address: Vec<EndpointAddress> = Vec::new();

    let svc_name = svc;
    let ep_slice_name = format!("metadata.name={svc_name}");
    let ep_filter = ListParams::default().fields(&ep_slice_name);

    let endpoints_list = ep_slices.list(&ep_filter).await.with_context(|| {
        format!(
            "Unable to list endpoint slices for filter : {:?}",
            ep_filter
        )
    })?;

    for ep in endpoints_list.iter() {
        println!("Endpoints for the Endpoint Slice : {:?} ", ep.name_any());
        for sub in ep.subsets.iter() {
            for addr in sub.iter() {
                // println!("Host : {:?}", addr);
                for host in addr.addresses.iter() {
                    for epa in host.iter() {
                        println!("Addresses: {:?}, hostname: {:?}", epa.ip, epa.hostname);
                        if !(ep_address.contains(epa)) {
                            ep_address.push(epa.clone());
                        }
                    }
                }
            }
        }
    }

    Ok(ep_address)
}
