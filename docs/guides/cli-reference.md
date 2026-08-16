# CLI Reference

A quick reference for the `diverge` CLI commands.

## `diverge dev`
Route cluster traffic for a service to your local machine.

```text
diverge dev [flags]
```
This command blocks until async routing is ready and syncs necessary environment variables (including async route variables) to your local environment.

## `diverge status`
Show active preview environments and preview groups.

```text
diverge status [flags]
```
Displays a summary of all active preview environments, preview groups, and their current state.

## `diverge logs`
Stream logs from a preview environment.

```text
diverge logs [environment-name]
```
Stream logs from pods in a preview environment. Shows logs from all services by default. You can use filters to narrow down the logs.

## `diverge env export`
Export environment variables for a service.

```text
diverge env export [flags]
```
Outputs environment variables required to run your service locally, mimicking the cluster configuration.

## `diverge providers list`
List all registered providers.

```text
diverge providers list
```
Outputs the available authentication and infrastructure providers registered with your Diverge installation.
