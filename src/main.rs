use futures::{StreamExt, join};
use k8s_openapi::{
    api::{
        core::v1::{Endpoints, Pod, Service, ServicePort, ServiceSpec, EndpointAddress},
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

async fn _query_pods(client: Client, ns: &str) -> anyhow::Result<()> {
    /*
        // Namespace where it will be looked
         let ns = "linkerd".to_string();

         query_pods(client.clone(), &ns).await.unwrap();

         query_services(client.clone(), &ns).await.unwrap();

         let linkerd_svc: Api<Endpoints> = Api::all(client.clone());
         for ep in linkerd_svc.list(&ListParams::default()).await? {
             println!("Ep for all : {}", ep.name_any());
         }
    */
    // Query the pods
    let pods = Api::<Pod>::namespaced(client, ns);
    for p in pods.list(&ListParams::default()).await? {
        // Pod IDs typically follow a <namespace>/<name> format
        println!("Pod {}/{}", ns, p.name_any());
    }

    // Implicit return
    Ok(())
}

async fn _query_services(client: Client, ns: &str) -> Result<(), Box<dyn std::error::Error>> {
    // Query the services
    let service = Api::<Service>::namespaced(client, ns);
    for svc in service.list(&ListParams::default()).await? {
        // Can also format like this if you think it's more readable
        println!("Service {ns}/{}", svc.name_any());
    }
    Ok(())
}

// Should always try to derive this at least
#[derive(Clone)]
pub struct Context(Client);

async fn create_global_svc(
    ctx: Arc<Context>,
    svc_name: String,
    ns: String,
    svc_port: Vec<ServicePort>,
) -> anyhow::Result<()> {
    /*
       - Create the global Service if it doesnt exist
       - Get the EndpointSlices from other Services
       - Add those EndpointSlices as backing to global Service.
    */

    let mut labels: BTreeMap<String, String> = BTreeMap::new();
    labels.insert("linkerd.io/service-mirror".to_string(), "true".to_string());
    labels.insert("linkerd.io/global-mirror".to_string(), "true".to_string());

    let svc_meta = ObjectMeta {
        labels: Some(labels.clone()),
        name: Some(format!("{svc_name}-svc-global")),
        namespace: Some(ns.to_string()),
        ..ObjectMeta::default()
    };

    let svc_spec = ServiceSpec {
        cluster_ip: Some("None".to_string()),
        ports: Some(svc_port),
        selector: Some(labels.clone()),
        ..Default::default()
    };

    let service_to_create = Service {
        metadata: svc_meta,
        spec: Some(svc_spec),
        ..Service::default()
    };

    // create service
    let svc: Api<Service> = Api::namespaced(ctx.0.clone(), &ns);

    // Why handle this here instead of bubbling up?
    match svc.create(&PostParams::default(), &service_to_create).await {
        Ok(tmp) => println!("Created the service : {}", tmp.name_any()),
        Err(e) => println!("Failed creating service : {}", e),
    }

    // Why do we return a result if we only return Ok?
    Ok(())
}

//Check if the global service exists
async fn check_if_aggregation_service_exists(
    svc: Arc<Service>,
    ctx: Arc<Context>,
) -> ObjectList<Service> {
    /*
        - Check if the label has key : mirror.linkerd.io/headless-mirror-svc-name
            - if yes it means that this is child service of parent service - mirror.linkerd.io/headless-mirror-svc-name
            else it means that this service is the parent service and it has childs
        
        - If global service doesnt exist i.e service sufix with '-global' for value of key : mirror.linkerd.io/headless-mirror-svc-name
        Make sure there should be only service
        
    */

    
    // Key which will be looked inside label
    let label_key = "mirror.linkerd.io/headless-mirror-svc-name".to_string();

    let mut parent_svc_name = String::new();
    
    // Check if the key exist inside labels
    if svc.labels().contains_key(&label_key) {
        match svc.labels().get(&label_key) {
            Some(parent_name) => parent_svc_name = parent_name.to_string(),
            _ => println!("Unable to get the parent name of service : {}", label_key),
        }
    } else {
        // If key doesnt exist inside label, that means name of the service is the current service
        parent_svc_name = svc.name_any();
    }   


    //Build up global service name
    let global_svc_name = format!("{parent_svc_name}-global");

    println!("Checking if the global service named : {global_svc_name} exists");

    let svc: Api<Service> = Api::all(ctx.0.clone());

    let label_filter = format!("metadata.name={global_svc_name}");

    let svc_filter = ListParams::default().fields(&label_filter);

    //check if it has any service named this global.
    return svc.list(&svc_filter).await.unwrap();

}

async fn list_svc_port(svc: Arc<Service>, ctx: Arc<Context>) -> Result<Vec<ServicePort>, Error> {
    println!("Listing the service port : {}", svc.name_any());

    let services: Api<Service> = Api::all(ctx.0.clone());

    let svc_name = format!("metadata.name={}", svc.name_any());

    let svc_filter = ListParams::default().fields(&svc_name);

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

    Ok(service_ports)

}
async fn list_endpoints(svc: Arc<Service>, ctx: Arc<Context>) -> Result<Vec<EndpointAddress>, Error> {
    /*
        - Try querying or simply listing all the endpoints for the service
        -  Filter the EndpointSlice on the name of Service Passed
    */
    let ep_slices: Api<Endpoints> = Api::all(ctx.0.clone());

    let mut ep_address: Vec<EndpointAddress> = Vec::new();

    let svc_name = svc.name_any();
    let ep_slice_name = format!("metadata.name={svc_name}");
    let ep_filter = ListParams::default().fields(&ep_slice_name);

    for ep in ep_slices.list(&ep_filter).await? {
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

    println!("Received the svc ports : {:?}", svc_ports.len());

    let svc_ep: Vec<EndpointAddress> = list_endpoints(svc.clone(), ctx.clone()).await?;

    println!("Recevied the EndpointAddresses : {:?}", svc_ep.len());

    let check_if_svc_exists = check_if_aggregation_service_exists(svc.clone(), ctx.clone()).await;


    println!(
        "Checking if aggregation svc exists {}",
        check_if_svc_exists.items.len()
    );
    

    if !check_if_svc_exists.items.len() > 0 {
        println!("Global service doesnt exist");

        let namespace: String = match svc.namespace() {
            Some(ns) => ns,
            None => "".to_string(),
        };
        if svc_ports.len() > 0 {
            println!("Creating global svc");
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
