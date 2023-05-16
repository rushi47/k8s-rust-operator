use clap::Parser;
use futures::StreamExt;
use k8s_openapi::api::core::v1::Service;
use kube::{
    runtime::{
        reflector::ObjectRef,
        watcher::{
            Config,
            Event::{Applied, Deleted},
        },
    },
    Api, Client, ResourceExt,
};

#[derive(Parser, Debug)]
#[command(author, version, about, long_about = None)]
struct Args {
    /// Log Level
    #[arg(
        short = 'v',
        long,
        env = "GLOBAL_MIRROR_LOG_LEVEL",
        default_value = "debug"
    )]
    log_level: String,

    // TODO: write a parser to support arbitrary time units (ms, s, m should be
    // enough)
    /// Requeue duration (in seconds)
    #[arg(long, default_value = "5")]
    _requeue_duration: usize,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let Args {
        log_level,
        _requeue_duration,
    } = Args::parse();

    let env_filter = tracing_subscriber::EnvFilter::builder()
        .with_regex(false)
        .parse(log_level)?;

    tracing_subscriber::fmt().with_env_filter(env_filter).init();

    // Create the client
    let client = Client::try_default()
        .await
        .expect("Failed to create client from env defaults");

    let svc = Api::<Service>::all(client.clone());
    let svc_filter = Config::default().labels("mirror.linkerd.io/mirrored-service=true");

    // Reflector store handles cached objects, writer needs to be sent to
    // watcher, reader half can be cloned as needed.
    let (mirror_services, mirror_svc_writer) = kube::runtime::reflector::store::<Service>();
    // Infinite stream
    let rf = kube::runtime::reflector(mirror_svc_writer, kube::runtime::watcher(svc, svc_filter));
    tokio::pin!(rf);
    while let Some(ev) = rf.next().await {
        // Stream will be terminated eagerly on error?
        let ev = ev?;
        match ev {
            Applied(svc) | Deleted(svc) => {
                if svc
                    .labels()
                    .contains_key("mirror.linkerd.io/headless-mirror-svc-name")
                {
                    // Do not process headless hostname services
                    continue;
                }

                // TODO: Need to filter on ClusterIP None, otherwise we get all mirror
                // services

                let has_global = {
                    // Get service name without cluster suffix
                    let svc_name = if let Some(name) = svc
                        .labels()
                        .get("mirror.linkerd.io/cluster-name".into())
                        .map(|cluster_name| svc.name_any().replace(cluster_name, "global"))
                    {
                        name
                    } else {
                        tracing::debug!(svc = %svc.name_any(), "mirror service missing annotation");
                        continue;
                    };

                    mirror_services.get(&ObjectRef::<Service>::new(&svc_name))
                };

                let global_service = has_global.unwrap_or_else(|| {
                    // create service
                    todo!();
                });
            }
            _ => {}
        }
    }

    Ok(())
}
