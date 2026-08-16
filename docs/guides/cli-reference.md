# CLI Reference

A quick reference for the `diverge` CLI commands.

## `diverge dev`
Route cluster traffic for a service to your local machine.

```bash
diverge dev [flags]
```
This command blocks until async routing is ready and syncs necessary environment variables (including async route variables) to your local environment.

## `diverge status`
Show active preview environments and preview groups.

```bash
diverge status [flags]
```
Displays a summary of all active preview environments, preview groups, and their current state.

## `diverge logs`
Stream logs from a preview environment.

```bash
diverge logs [environment-name]
```
Stream logs from pods in a preview environment. Shows logs from all services by default. You can use filters to narrow down the logs.

## `diverge env export`
Export environment variables for a service.

```bash
diverge env export [flags]
```
Outputs environment variables required to run your service locally, mimicking the cluster configuration.

## `diverge providers list`
List all registered providers.

```bash
diverge providers list
```
Outputs the available authentication and infrastructure providers registered with your Diverge installation.
