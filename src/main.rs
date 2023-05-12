use futures::{join, StreamExt};
use k8s_openapi::{
    api::{
        core::v1::{EndpointAddress, Endpoints, Pod, Service, ServicePort, ServiceSpec},
        discovery::v1::{Endpoint, EndpointSlice},
    },
    List,
};
use kube::{
    api::{Api, ListParams, PostParams, ResourceExt},
    core::ObjectMeta,
    runtime::{controller::Action, watcher::Config},
    Client, Error,
};
use kube::{core::ObjectList, runtime::Controller};
use std::{collections::BTreeMap, sync::Arc, time::Duration};

mod util;
use crate::util::{create_global_svc, check_if_aggregation_service_exists, list_endpoints, list_svc_port};

// Should always try to derive this at least
#[derive(Clone)]
pub struct Context(Client);

async fn reconciler(svc: Arc<Service>, ctx: Arc<Context>) -> Result<Action, Error> {
    /*
    Step 1: Create the global / aggregation service, which has backing from respective ordinal services.
        - Reconcile for the svc received, i.e its being watched on.

        - Check if the global/Aggregation svc for the respective services exists

        - If the aggregation svc exist :
            Check if the ports, endpointslices of the SVC for which this reconciler has been called
            are added to global/aggregation svc.
                - handle duplicates
                - handle add or remove

    */


    //Query the ports associated with this service.
    let svc_ports = list_svc_port(svc.clone(), ctx.clone()).await?; 

    //Check if the aggregation service exists
    let (if_svc_exists, global_svc_name) = check_if_aggregation_service_exists(svc.clone(), ctx.clone()).await;
    
    //If Service does exist create it. 
    if !if_svc_exists {
        
        let namespace: String = match svc.namespace() {
            Some(ns) => ns,
            None => "".to_string(),
        };

        println!("Creating global service with name : [X] {}", global_svc_name);

        let global_svc =  create_global_svc(ctx.clone(), global_svc_name, namespace, svc_ports).await.expect("Issue creating global service");
    
    }

    let svc_ep: Vec<EndpointAddress> = list_endpoints(svc.clone(), ctx.clone()).await?;



    Ok(Action::await_change())
}

fn error_policy(svc: Arc<Service>, err: &Error, ctx: Arc<Context>) -> Action {
    Action::requeue(Duration::from_secs(5))
}

#[tokio::main]
async fn main() {
    // Create the client
    let client = Client::try_default()
        .await
        .expect("Failed to create client from env defaults");

    let linkerd_svc = Api::all(client.clone());

    let context: Arc<Context> = Arc::new(Context(client.clone()));

    Controller::new(
        linkerd_svc.clone(),
        Config::default().labels("mirror.linkerd.io/mirrored-service=true"),
    )
    .run(reconciler, error_policy, context)
    .for_each(|reconciliation_result| async move {
        match reconciliation_result {
            Ok(linkerd_svc_resource) => {
                // println!("Received the resource : {:?}", linkerd_svc_resource)
            }
            Err(err) => println!("Received error in reconcilation : {}", err),
        }
    })
    .await;
}
