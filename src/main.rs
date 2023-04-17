use kube::{Client, api::{Api, ListParams, ResourceExt}};
use k8s_openapi::api::core::v1::{Pod, Service};

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

#[tokio::main]
async fn main()  -> Result<(), Box<dyn std::error::Error>> {
    
    //Create the client
    let client = Client::try_default().await.unwrap();
    
    //Namespace where it will be looked
    let ns = "linkerd".to_string();

    query_pods(client.clone(), &ns).await.unwrap();
    
    query_services(client.clone(), &ns).await.unwrap();

    Ok(())
    
}