use kube::{Client, Error, api::{Api, ListParams, ResourceExt}, runtime::{watcher::Config, controller::Action}};
use k8s_openapi::{api::core::v1::{Pod, Service}};
use std::{sync::Arc, time::Duration};
use kube::runtime::{Controller};
use futures::StreamExt;

async fn query_pods(client:Client, ns: &str) -> Result<(), Box<dyn std::error::Error>> {
    
    //Query the pods
    let pods: Api<Pod> = Api::namespaced(client, ns);
    for p in pods.list(&ListParams::default()).await?{
        println!("Pod from the {} namespace : {:?}", ns, p.name_any());
    }
    return Ok(())
}

async fn query_services(client:Client, ns: &str) -> Result<(), Box<dyn std::error::Error>> { 
    //Query the services
    let service: Api<Service> = Api::namespaced(client, ns);
    for svc in service.list(&ListParams::default()).await?{
        println!("Service name from the {} namespace : {:?}", ns, svc.name_any());
    }
    return Ok(())
}

struct Context {
    client: Client,
}
async fn reconciler(svc: Arc<Service>, ctx: Arc<Context>) -> Result<Action, Error> {
    
    if let Some(ts) = &svc.metadata.deletion_timestamp {
        println!("Service : {} being delete in time : {:?}", svc.name_any(), &ts);
    }
    
    Ok(Action::await_change())
}

fn error_policy(svc:Arc<Service>, err: &Error ,ctx:Arc<Context>) -> Action {

    Action::requeue(Duration::from_secs(5))
}

#[tokio::main]
async fn main()  -> Result<(), Box<dyn std::error::Error>> {
    
    //Create the client
    let client = Client::try_default().await.unwrap();
    
    //Namespace where it will be looked
    let ns = "linkerd".to_string();

    // query_pods(client.clone(), &ns).await.unwrap();
    
    // query_services(client.clone(), &ns).await.unwrap();

    let linkerd_svc: Api<Service> = Api::all(client.clone());

    let context: Arc<Context> = Arc::new(Context { client: client.clone() });

    Controller::new(linkerd_svc.clone(), Config::default().labels("app=cockroachdb"))
    .run(reconciler, error_policy, context)
    .for_each(|reconciliation_result| async move {
        match reconciliation_result {
            Ok(linkerd_svc_resource) => println!("Received the resource : {:?}", linkerd_svc_resource),
            Err(err) => println!("Received error in reconcilation : {}", err),
        }
    }).await;

    Ok(())
}