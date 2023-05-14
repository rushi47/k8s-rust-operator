use futures::StreamExt;
use k8s_openapi::api::core::v1::Service;
use kube::runtime::Controller;
use kube::{
    api::{Api, ResourceExt},
    runtime::{controller::Action, watcher::Config},
    Client, Error,
};
use std::{sync::Arc, time::Duration};

mod util;
use crate::global_mirror::endpoints::create_ep_slice;
use crate::util::{
    check_if_aggregation_service_exists, check_if_ep_slice_exist, get_cluster_name, get_parent_name,
};

mod global_mirror;
use global_mirror::service::create_global_svc;
use log::{debug, error, info};
use log4rs;

const REQUE_DURATION: Duration = Duration::from_secs(5);
const LOGGER_NAME: &str = "mirror-logger";
const LOG_CONFIG: &str = "conf/lg4rs.yml";

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

    //[Optional Optimisation] Only reconcile for headless service, as we can get all the information from headless service itself.
    //Dont reconcile for it individual sets.
    let label_key_svc_name = "mirror.linkerd.io/headless-mirror-svc-name".to_string();
    if svc.labels().contains_key(&label_key_svc_name) {
        return Ok(Action::await_change());
    }

    //Check if the aggregation service exists
    let (if_svc_exists, global_svc_name) =
        match check_if_aggregation_service_exists(svc.clone(), ctx.clone()).await {
            Ok((if_svc_exists, global_svc_name)) => {
                debug!(
                    target: LOGGER_NAME,
                    "Aggregation service exists by name : {}, skipping creation", global_svc_name
                );
                (if_svc_exists, global_svc_name)
            }
            Err(e) => {
                error!(target: LOGGER_NAME, "{:?}", e);
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
            match create_global_svc(ctx.clone(), &svc, global_svc_name.clone(), namespace).await {
                Ok(global_svc) => {
                    info!(
                        target: LOGGER_NAME,
                        "Global service created succesfully with name : {}",
                        global_svc.name_any()
                    );
                    global_svc
                }
                Err(e) => {
                    error!(target: LOGGER_NAME, "{:?}", e);
                    return Ok(Action::requeue(REQUE_DURATION));
                }
            };
    }

    //Get the headless service name of mirrored target service to directly get the Endpoint
    //TO DO: if we keep check of only allowing target headless service, we can remove this.
    let _headless_svc = match get_parent_name(svc.clone()) {
        Ok(svc_name) => {
            debug!(
                target: LOGGER_NAME,
                "Get the parent name for this service : {}, parent/headless svc: {}",
                svc.name_any().clone(),
                svc_name
            );
            svc_name
        }
        Err(e) => {
            error!(
                target: LOGGER_NAME,
                "Unable to get the parent service name : {}", e
            );
            return Ok(Action::requeue(REQUE_DURATION));
        }
    };

    //Check if endpointSlice exists
    //Build Endpointslice name : Ex. X-global-target
    let svc_cluster_name = get_cluster_name(&svc);

    let eps_name = format!("{}-{}", global_svc_name.clone(), svc_cluster_name.clone());

    let eps_if_exists = match check_if_ep_slice_exist(ctx.clone(), eps_name.clone()).await {
        Ok(exists) => exists,
        Err(e) => {
            error!(target: LOGGER_NAME, "{:?}", e);
            return Ok(Action::requeue(REQUE_DURATION));
        }
    };

    //Create the EndpointSlice if doesn't exists
    if !eps_if_exists {
        //TO DO: If first check is removed of allowing only headless service then headless_svc need to retrieved for second param.
        let endpoint_slice =
            match create_ep_slice(ctx.clone(), &svc, global_svc_name, svc_cluster_name.clone())
                .await
            {
                Ok(eps) => eps,
                Err(e) => {
                    error!(target: LOGGER_NAME, "{:?}", e);
                    return Ok(Action::requeue(REQUE_DURATION));
                }
            };

        info!(
            target: LOGGER_NAME,
            "EndpointSlice is created : {:?}",
            endpoint_slice.name_any()
        );
    }

    info!(
        target: LOGGER_NAME,
        "Reconciled succesfully, Global EndpointSlice & Service should exists."
    );
    Ok(Action::await_change())
}

fn error_policy(_svc: Arc<Service>, _err: &Error, _ctx: Arc<Context>) -> Action {
    Action::requeue(Duration::from_secs(5))
}

#[tokio::main]
async fn main() {
    //Initailise logger
    log4rs::init_file(LOG_CONFIG, Default::default()).unwrap();

    info!(target: LOGGER_NAME, "Starting Global Mirror .. !!");

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
            Err(err) => error!(
                target: LOGGER_NAME,
                "Received error in reconcilation : {:?}", err
            ),
        }
    })
    .await;
}
