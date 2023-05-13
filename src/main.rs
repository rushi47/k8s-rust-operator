use futures::StreamExt;
use k8s_openapi::api::core::v1::{EndpointAddress, Service};
use kube::runtime::Controller;
use kube::{
    api::{Api, ResourceExt},
    runtime::{controller::Action, watcher::Config},
    Client, Error,
};
use std::{sync::Arc, time::Duration};

mod util;
use crate::util::{
    check_if_aggregation_service_exists, get_parent_name, list_endpoints, list_svc_port,
};

mod global_mirror;
use global_mirror::service::create_global_svc;

const REQUE_DURATION: Duration = Duration::from_secs(5);

// Should always try to derive this at least
#[derive(Clone)]
pub struct Context(Client);

async fn reconciler(svc: Arc<Service>, ctx: Arc<Context>) -> Result<Action, Error> {
    /*
    * Create the global / aggregation service, which has backing from respective ordinal services.
        - Reconcile for the svc received, i.e its being watched on.
        - Check if the global/Aggregation svc for the respective services exists
        - If the aggregation svc doexnt exist, create it
        - If Global service exist , check if the ports, endpointslices of the SVC for which this reconciler has been called
            are added to global/aggregation svc.
                - handle duplicates
                - handle add or remove
    */

    //Check if the aggregation service exists
    let (if_svc_exists, global_svc_name) =
        match check_if_aggregation_service_exists(svc.clone(), ctx.clone()).await {
            Ok((if_svc_exists, global_svc_name)) => (if_svc_exists, global_svc_name),
            Err(e) => {
                eprintln!("Unable to check if aggregation service exist : {}", e);
                return Ok(Action::requeue(REQUE_DURATION));
            }
        };

    //Query the ports associated with this service.
    let svc_ports = match list_svc_port(svc.clone(), ctx.clone()).await {
        Ok(svc_ports) => svc_ports,
        Err(e) => {
            //If the is issue in listing ports back out of the function, dont do anything.
            eprintln!("Issue in listing service ports : {}", e);
            return Ok(Action::requeue(REQUE_DURATION));
        }
    };

    //If Service doesn't exist create it.
    if !if_svc_exists {
        let namespace: String = match svc.namespace() {
            Some(ns) => ns,
            None => "".to_string(),
        };

        let _global_svc =
            match create_global_svc(ctx.clone(), global_svc_name.clone(), namespace, svc_ports)
                .await
            {
                Ok(global_svc) => {
                    println!(
                        "Global service created succesfully with name : {}",
                        global_svc.name_any()
                    );
                    global_svc
                }
                Err(e) => {
                    eprintln!("Issue in creating service : {}", e);
                    return Ok(Action::requeue(REQUE_DURATION));
                }
            };
    }

    //Get the headless service name of mirrored target service to directly get the Endpoint
    let headless_svc = match get_parent_name(svc.clone()).await {
        Ok(svc_name) => svc_name,
        Err(e) => {
            eprintln!("Unable to get the parent service name : {}", e);
            return Ok(Action::requeue(REQUE_DURATION));
        }
    };

    //Create the EndpointSlice
    let _svc_ep: Vec<EndpointAddress> = match list_endpoints(headless_svc, ctx.clone()).await {
        Ok(svc_ep) => svc_ep,
        Err(e) => {
            eprintln!("Issue in listing endpoints : {}", e);
            return Ok(Action::requeue(REQUE_DURATION));
        }
    };

    Ok(Action::await_change())
}

fn error_policy(_svc: Arc<Service>, _err: &Error, _ctx: Arc<Context>) -> Action {
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
            Ok(_linkerd_svc_resource) => {
                // println!("Received the resource : {:?}", linkerd_svc_resource)
            }
            Err(err) => println!("Received error in reconcilation : {}", err),
        }
    })
    .await;
}
