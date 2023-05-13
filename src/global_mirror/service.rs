use anyhow::Context as anyhow_context;
use k8s_openapi::api::core::v1::{Service, ServicePort, ServiceSpec};
use kube::{
    api::{Api, ListParams, PostParams},
    core::ObjectMeta,
    ResourceExt,
};
use std::{collections::BTreeMap, sync::Arc};

use super::super::Context;
use ::anyhow::Result;

pub async fn list_svc_port(ctx: Arc<Context>, svc: &Arc<Service>) -> Result<Vec<ServicePort>> {
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

pub async fn create_global_svc(
    ctx: Arc<Context>,
    svc: &Arc<Service>,
    global_svc_name: String,
    ns: String,
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

    let svc_port: Vec<ServicePort> = list_svc_port(ctx.clone(), &svc).await?;

    let svc_meta = ObjectMeta {
        labels: Some(labels.clone()),
        name: Some(global_svc_name.clone()),
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
        .await
        .with_context(|| {
            format!(
                "Unable to create global service with service name : {}",
                global_svc_name
            )
        })?;

    Ok(service_to_create)
}
