# WSO2 API Discovery Server (ADS)

Discovers unmanaged APIs in runtime traffic, compares them against APIs registered in WSO2 API Manager, and surfaces the gaps inside the APIM Admin Portal as a new "Unmanaged APIs" tab under the Governance section.

## Quick start

```bash
make build
./bin/ads --validate --config config/config.toml.example
./bin/ads --config config/config.toml.example
```

## Architecture

Three deployable components:

1. ADS daemon (this repo) — Go service that observes traffic via DeepFlow and reconciles against APIM.
2. carbon-apimgt fork — adds a Backend-for-Frontend resource group under `/api/am/admin/v4/governance/discovery/*`.
3. apim-apps fork — adds the "Unmanaged APIs" tab in the admin portal Governance section.

## Requirements

- Go 1.22+
- PostgreSQL 13+
- DeepFlow 6.4+ for traffic observation
- WSO2 APIM 4.6.0+ (JDK 21 runtime)

## License

Apache 2.0.
