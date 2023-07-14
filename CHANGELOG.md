# Changelog

## v1.1.2

- fix rpm dependencies

## v1.1.1

- add support for unix domain sockets
- build linux packages

## v1.1.0

 - prefix container labels with `container.`

## v1.0.3

 - Bugfix: Blackhole and DNS container isn't reverted properly when container failed (and not the pod)

## v1.0.2

 - New: new container.image attributes for registry, repository, and tag
 - Improvement: Logging improved when container couldn't stop because it wasn't found
 - Improvement: Error message for failures when starting stress-ng attacks
 - Bugfix: Fixed unique container ids for sidecar containers in same pod
 - Bugfix: Removing trailing / in container.name
 - Bugfix: Datatype for stop-container's `graceful` parameter
 - Bugfix: Blackhole container isn't reverted properly when container failed (and not the pod)

## v1.0.1

 - Bugfixes
 - Conflicting ports when using with extension-host

## v1.0.0

 - Initial release
