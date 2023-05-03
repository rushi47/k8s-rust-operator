use futures::StreamExt;
use k8s_openapi::{
    api::{
        core::v1::{Endpoints, Pod, Service, ServicePort, ServiceSpec},
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

async fn _query_pods(client: Client, ns: &str) -> Result<(), Box<dyn std::error::Error>> {
    /*
        //Namespace where it will be looked
         let ns = "linkerd".to_string();

         query_pods(client.clone(), &ns).await.unwrap();

         query_services(client.clone(), &ns).await.unwrap();

         let linkerd_svc: Api<Endpoints> = Api::all(client.clone());
         for ep in linkerd_svc.list(&ListParams::default()).await? {
             println!("Ep for all : {}", ep.name_any());
         }
    */
    //Query the pods
    let pods: Api<Pod> = Api::namespaced(client, ns);
    for p in pods.list(&ListParams::default()).await? {
        println!("Pod from the {} namespace : {:?}", ns, p.name_any());
    }
    return Ok(());
}

async fn _query_services(client: Client, ns: &str) -> Result<(), Box<dyn std::error::Error>> {
    //Query the services
    let service: Api<Service> = Api::namespaced(client, ns);
    for svc in service.list(&ListParams::default()).await? {
        println!(
            "Service name from the {} namespace : {:?}",
            ns,
            svc.name_any()
        );
    }
    return Ok(());
}

pub struct Context {
    client: Client,
}

async fn create_global_svc(
    ctx: Arc<Context>,
    svc_name: String,
    ns: String,
    svc_port: Vec<ServicePort>,
) -> Result<(), Error> {
    /*
       - Create the global Service if it doesnt exist
       - Get the EndpointSlices from other Services
       - Add those EndpointSlices as backing to global Service.
    */

    let mut labels: BTreeMap<String, String> = BTreeMap::new();
    labels.insert("linkerd.io/service-mirror".to_string(), "true".to_string());
    labels.insert("linkerd.io/global-mirror".to_string(), "true".to_string());

    let svc_meta: ObjectMeta = ObjectMeta {
        labels: Some(labels.clone()),
        name: Some(format!("{svc_name}-svc-global")),
        namespace: Some(ns.to_string()),
        ..ObjectMeta::default()
    };

    let svc_spec: ServiceSpec = ServiceSpec {
        cluster_ip: Some("None".to_string()),
        ports: Some(svc_port),
        selector: Some(labels.clone()),
        ..Default::default()
    };

    let ServiceToCreate: Service = Service {
        metadata: svc_meta,
        spec: Some(svc_spec),
        ..Service::default()
    };

    //create service
    let svc: Api<Service> = Api::namespaced(ctx.client.clone(), &ns);

    match svc.create(&PostParams::default(), &ServiceToCreate).await {
        Ok(tmp) => println!("Created the service : {}", tmp.name_any()),
        Err(e) => println!("Failed creating service : {}", e),
    }
    Ok(())
}

//Check if the global service exists
async fn check_if_aggregation_service_exists(
    svc: Arc<Service>,
    ctx: Arc<Context>,
) -> ObjectList<Service> {
    /*
        - Simply check if the service named with suffix or the name decided exists, if list isnt empty it exists.
    */

    let name_svc = "cockroach-db-global".to_string();

    let svc: Api<Service> = Api::all(ctx.client.clone());

    let label_filter = format!("app={name_svc}");

    let svc_filter = ListParams::default().labels(&label_filter);

    //check if it has any service named this global.
    return svc.list(&svc_filter).await.unwrap();
}

async fn list_svc_port(svc: Arc<Service>, ctx: Arc<Context>) -> Result<Vec<ServicePort>, Error> {
    println!("Listing the service port : {}", svc.name_any());

    let services: Api<Service> = Api::all(ctx.client.clone());

    let svc_name = format!("app={}", svc.name_any());

    let svc_filter = ListParams::default().labels(&svc_name);

    let mut service_ports: Vec<ServicePort> = Vec::new();

    for svc in services.list(&svc_filter).await? {
        println!("Name of the servcice : {:?} ", svc.name_any());
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
                _ => println!("No service port found"),
            }
        }
    }

    return Ok(service_ports);
}
async fn list_endpoints(svc: Arc<Service>, ctx: Arc<Context>) -> Result<(), Error> {
    /*
        - Try querying or simply listing all the endpoints for the service
        -  Filter the EndpointSlice on the name of Service Passed
    */
    let ep_slices: Api<Endpoints> = Api::all(ctx.client.clone());

    let svc_name = svc.name_any();
    let ep_slice_name = format!("app={svc_name}");

    let ep_filter = ListParams::default().labels(&ep_slice_name);
    for ep in ep_slices.list(&ep_filter).await? {
        println!("Endpoints for the Endpoint Slice : {:?} ", ep.name_any());
        for sub in ep.subsets.iter() {
            for addr in sub.iter() {
                // println!("Host : {:?}", addr);
                for host in addr.addresses.iter() {
                    for epa in host.iter() {
                        println!("Addresses: {:?}, hostname: {:?}", epa.ip, epa.hostname);
                    }
                }
            }
        }
    }

    Ok(())
}

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

    if let Some(ts) = &svc.metadata.deletion_timestamp {
        println!(
            "Service : {} being delete in time : {:?}",
            svc.name_any(),
            &ts
        );
        //remove finalizers and return from here
    }

    let svc_ports = list_svc_port(svc.clone(), ctx.clone()).await?;

    println!("Received the svc ports : {:?}", svc_ports);

    list_endpoints(svc.clone(), ctx.clone()).await?;

    let check_if_svc_exists = check_if_aggregation_service_exists(svc.clone(), ctx.clone()).await;

    println!(
        "Checking if aggregation svc exists {}",
        check_if_svc_exists.items.len()
    );
    if !check_if_svc_exists.items.len() > 0 {
        println!("Create the global svc");

        let namespace: String = match svc.namespace() {
            Some(ns) => ns,
            None => "".to_string(),
        };
        if svc_ports.len() > 1 {
            create_global_svc(ctx, svc.name_any(), namespace, svc_ports)
                .await
                .unwrap();
        }
    }

    Ok(Action::await_change())
}

fn error_policy(svc: Arc<Service>, err: &Error, ctx: Arc<Context>) -> Action {
    Action::requeue(Duration::from_secs(5))
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    //Create the client
    let client = Client::try_default().await.unwrap();

    let linkerd_svc: Api<Service> = Api::all(client.clone());

    let context: Arc<Context> = Arc::new(Context {
        client: client.clone(),
    });

    Controller::new(
        linkerd_svc.clone(),
        Config::default().labels("app=cockroachdb"),
    )
    .run(reconciler, error_policy, context)
    .for_each(|reconciliation_result| async move {
        match reconciliation_result {
            Ok(linkerd_svc_resource) => {
                println!("Received the resource : {:?}", linkerd_svc_resource)
            }
            Err(err) => println!("Received error in reconcilation : {}", err),
        }
    })
    .await;

    Ok(())
}
