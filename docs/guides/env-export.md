# Environment Export Guide

The `diverge env export` CLI command allows developers to easily extract environment variables from baseline environment pods. This is particularly useful when you need to run a service locally and connect it to the same dependencies (databases, APIs, caches) that the baseline environment uses.

## Usage

```bash
diverge env export --service <service-name> [flags]
```

### Flags
- `--service`: (Required) The name of the service to extract variables from.
- `--format`: The output format (`dotenv`, `json`, `shell`). Defaults to `dotenv`.
- `--output`: File to write the output to. If omitted, prints to standard output (stdout).

## Output Formats

You can choose how the environment variables are formatted depending on your IDE or workflow requirements.

### Dotenv (Default)
Useful for local development servers or Docker Compose.
```bash
$ diverge env export --service payments --format dotenv
DATABASE_URL="postgres://user:pass@host/db"
API_KEY="my-secret-key"
PORT=8080
```

### JSON
Useful for programmatic consumption or IDE integration (like VSCode launch configurations).
```bash
$ diverge env export --service payments --format json
{
  "API_KEY": "my-secret-key",
  "DATABASE_URL": "postgres://user:pass@host/db",
  "PORT": "8080"
}
```

### Shell
Useful for directly sourcing into your terminal session.
```bash
$ diverge env export --service payments --format shell
export API_KEY="my-secret-key"
export DATABASE_URL="postgres://user:pass@host/db"
export PORT="8080"
```

## Workflows

### Writing to a file
You can write directly to a `.env` file using the `--output` flag (or standard shell redirection):

```bash
# Using standard redirection
diverge env export --service frontend > .env.preview

# Using the output flag
diverge env export --service frontend --output .env.preview
```

### Integrating with IDEs
If you want to quickly source the environment variables in your current shell session before running your application locally, you can use:

```bash
eval $(diverge env export --service payments --format shell)
go run main.go
```
