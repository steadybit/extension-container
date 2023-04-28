# Steadybit extension-container

TODO describe what your extension is doing here from a user perspective.

## Configuration

| Environment Variable | Meaning | Default |
|----------------------|---------|---------|
| `TODO`               | Todo    |         |

The extension supports all environment variables provided by [steadybit/extension-kit](https://github.com/steadybit/extension-kit#environment-variables).

## Running the Extension

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
