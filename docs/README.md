# DuckStream Documentation

Welcome to DuckStream documentation. Here's a quick overview of the available guides:

## Getting Started

- **[Quick Start](./README.md)** - Get up and running quickly
- **[Architecture](./docs/architecture.md)** - System design and component interactions
- **[API Reference](./docs/api.md)** - HTTP API endpoint documentation

## Usage

- **[Query Language](./docs/queries.md)** - SQL query patterns and examples
- **[Configuration](./docs/configuration.md)** - Customizing server behavior

## Guides

### Monitoring
The system exposes metrics at `GET /metrics` for integration with Prometheus, Grafana, or similar tools.

### Scaling
For high-throughput scenarios, consider:
- Increasing `MaxClients` for more concurrent connections
- Adjusting `PollInterval` for more/less frequent polling
- Using connection pooling for query validation

## Quick Links

| Topic | Description |
|-------|-------------|
| [Architecture](./docs/architecture.md) | Deep dive into system design |
| [API](./docs/api.md) | HTTP API endpoints and examples |
| [Queries](./docs/queries.md) | SQL query patterns and best practices |