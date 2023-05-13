use k8s_openapi::api::core::v1::{Service, ServicePort, ServiceSpec};
use kube::{
    api::{Api, PostParams},
    core::ObjectMeta,
};
use std::{collections::BTreeMap, sync::Arc};

use super::super::Context;
use ::anyhow::Result;


pub async fn create_global_svc(
    ctx: Arc<Context>,
    global_svc_name: String,
    ns: String,
    svc_port: Vec<ServicePort>,
) -> Result<Service> {
    /*
       - Create the global Service if it doesnt exist
       - Get the EndpointSlices from other Services
       - Add those EndpointSlices as backing to global Service.
    */

    let mut labels: BTreeMap<String, String> = BTreeMap::new();

    //Add the label of source service its mirroring for.
    labels.insert(
        "mirror.linkerd.io/global-mirror-of".to_string(),
        global_svc_name.replace("-global", ""),
    );

    println!("Creating global service with name : {}", global_svc_name);

    let svc_meta = ObjectMeta {
        labels: Some(labels.clone()),
        name: Some(global_svc_name),
        namespace: Some(ns.to_string()),
        ..ObjectMeta::default()
    };

    let svc_spec = ServiceSpec {
        cluster_ip: Some("None".to_string()),
        ports: Some(svc_port),
        ..Default::default()
    };

    let service_to_create = Service {
        metadata: svc_meta,
        spec: Some(svc_spec),
        ..Service::default()
    };

    // create service
    let svc: Api<Service> = Api::namespaced(ctx.0.clone(), &ns);

    svc.create(&PostParams::default(), &service_to_create)
        .await?;

    Ok(service_to_create)
}
