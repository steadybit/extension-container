<img src="./logo.svg" height="130" align="right" alt="Container logo">

# Steadybit extension-container

This [Steadybit](https://www.steadybit.com/) extension provides a host discovery and the various actions for container targets.

Learn about the capabilities of this extension in our [Reliability Hub](https://hub.steadybit.com/extension/com.github.steadybit.extension_container).

## Configuration

| Environment Variable                       | Helm value                                             | Meaning                                                                                                                    | Required | Default |
|--------------------------------------------|--------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------|----------|---------|
| `STEADYBIT_EXTENSION_RUNTIME`              | `container.runtime`                                    | The container runtime to user either `docker`, `containerd` or `cri-o`. Will be automatically configured if not specified. | yes      | (auto)  |
| `STEADYBIT_EXTENSION_SOCKET`               | `containerRuntimes.(docker/containerd/cri-o).socket`   | The socket used to connect to the container runtime. Will be automatically configured if not specified.                    | yes      | (auto)  |
| `STEADYBIT_EXTENSION_CONTAINERD_NAMESPACE` |                                                        | The containerd namespace to use.                                                                                           | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_RUNC_ROOT`            | `containerRuntimes.(docker/containerd/cri-o).runcRoot` | The runc root to use.                                                                                                      | yes      | (auto)  |
| `STEADYBIT_EXTENSION_RUNC_DEBUG`           |                                                        | Activate debug mode for run.                                                                                               | yes      | k8s.io  |

The extension supports all environment variables provided by [steadybit/extension-kit](https://github.com/steadybit/extension-kit#environment-variables).

## Installation

### Using Docker

```sh
$ docker run \
  --rm \
  -p 8080 \
  --privileged \
  --pid=host \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /run/docker/runtime-runc/moby:/run/docker/runtime-runc/moby\
  -v /sys/fs/cgroup:/sys/fs/cgroup\
  --name steadybit-extension-container \
  ghcr.io/steadybit/extension-container:latest
```

### Using Helm in Kubernetes

```sh
$ helm repo add steadybit-extension-container https://steadybit.github.io/extension-container
$ helm repo update
$ helm upgrade steadybit-extension-container \
    --install \
    --wait \
    --timeout 5m0s \
    --create-namespace \
    --namespace steadybit-extension \
    --set container.runtime=docker \
    steadybit-extension-container/steadybit-extension-container
```

## Register the extension

Make sure to register the extension at the steadybit platform. Please refer to
the [documentation](https://docs.steadybit.com/integrate-with-steadybit/extensions/extension-installation) for more information.

## Anatomy of the extension / Security

We try to limit the needed access needed for the extension to the absolute minimum. So the extension itself can run as a non-root user on a read-only root file-system and will by default if deployed using the provided helm-chart.
In order do execute certain actions the extension needs certain capabilities.

### discovery / state attacks

For discovery and executing state attacks such as stop or pause container the extension needs access to the container runtime socket.

### resource and network attacks

Resource attacks starting stress-ng processes, the network attacks are starting ip or tc processes as runc container reusing the target container's linux namespace(s), control group(s) and user.
This requires the following capabilities: SYS_ADMIN, SYS_RESOURCE, SYS_PTRACE, KILL, NET_ADMIN, DAC_OVERRIDE, SETUID, SETGID, AUDIT_WRITE.
The needed binaries are included in the extension container image.
