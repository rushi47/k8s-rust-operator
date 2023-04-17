use kube::{Client, api::{Api, ListParams, ResourceExt}};
use k8s_openapi::api::core::v1::Pod;

#[tokio::main]
async fn main()  -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::try_default().await.unwrap();
    let pods: Api<Pod> = Api::default_namespaced(client);
    for p in pods.list(&ListParams::default()).await?{
        println!("Pod from the default namespace : {:?}", p.name_any());
    }

    Ok(())
    
}