# Steadybit extension-container

This [Steadybit](https://www.steadybit.com/) extension provides a container discovery and various actions for container targets.

Learn about the capabilities of this extension in our [Reliability Hub](https://hub.steadybit.com/extension/com.github.steadybit.extension_container).

## Configuration

| Environment Variable | Helm value          | Meaning                                                | Required | Default      |
|----------------------|---------------------|--------------------------------------------------------|----------|--------------|
|                      | `container.runtime` | The Container-Runtime (`docker`, `containerd`, `crio`) | yes      | `containerd` |

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
    --set container.runtimer docker \
    steadybit-extension-container/steadybit-extension-container
```

## Register the extension

Make sure to register the extension at the steadybit platform. Please refer to
the [documentation](https://docs.steadybit.com/integrate-with-steadybit/extensions/extension-installation) for more information.
