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
  --name steadybit-extension-containe \
  ghcr.io/steadybit/extension-containe:latest
```

### Using Helm in Kubernetes

```sh
$ helm repo add steadybit-extension-containe https://steadybit.github.io/extension-containe
$ helm repo update
$ helm upgrade steadybit-extension-containe \
    --install \
    --wait \
    --timeout 5m0s \
    --create-namespace \
    --namespace steadybit-extension \
    steadybit-extension-containe/steadybit-extension-containe
```
