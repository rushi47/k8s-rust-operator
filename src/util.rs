use k8s_openapi::api::{core::v1::Service, discovery::v1::EndpointSlice};
use kube::api::{Api, ListParams, ResourceExt};
use std::sync::Arc;

use ::anyhow::Result;
use anyhow::Context as anycontext;

use super::Context;

pub async fn get_parent_name(svc: Arc<Service>) -> Result<String> {
    // Get the name of headless service.
    let label_key_svc_name = "mirror.linkerd.io/headless-mirror-svc-name".to_string();

    let mut _parent_svc_name = String::new();

    // Check if the key exist inside labels
    if svc.labels().contains_key(&label_key_svc_name) {
        _parent_svc_name = match svc.labels().get(&label_key_svc_name) {
            Some(svc_name) => svc_name.clone(),
            _ => svc.name_any(),
        }
    } else {
        // If key doesnt exist inside label, that means name of the service is the current service
        _parent_svc_name = svc.name_any();
    }

    Ok(_parent_svc_name)
}

pub fn get_cluster_name(svc: &Arc<Service>) -> String {
    /*
       Get cluster name of where this service belongs from labels
    */

    let cluster_name = match svc.labels().get("mirror.linkerd.io/cluster-name") {
        Some(cluster_name) => cluster_name.to_string(),
        None => String::new(),
    };

    cluster_name
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

    println!("Checking if the global service with name exists : {global_svc_name}");

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

    //Global service exist by the name, should only exists one service.
    if svc_received.items.len() == 1 {
        return Ok((true, global_svc_name));
    }

    Ok((false, global_svc_name))
}

pub async fn check_if_ep_slice_exist(ctx: Arc<Context>, eps_name: String) -> Result<bool> {
    /*
        - Check if endpointslice exists
    */
    println!("Checking if eps exist with Name : {}", eps_name.clone());
    let eps: Api<EndpointSlice> = Api::all(ctx.0.clone());

    let eps_filter = format!("metadata.name={}", eps_name);
    let filter = ListParams::default().fields(&eps_filter);

    let eps_received = eps
        .list(&filter)
        .await
        .with_context(|| format!("Unable to list EndpointSlice for name : {}", eps_name))?;

    //Should on exists one endpointslice with respective name if more than one found.
    if eps_received.items.len() == 1 {
        return Ok(true);
    }

    Ok(false)
}
