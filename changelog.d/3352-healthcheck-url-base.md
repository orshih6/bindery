### Fixed
- **The Docker healthcheck now works with `BINDERY_URL_BASE`** (#3352). With a path prefix set, the container's built in healthcheck still asked for `/api/v1/health` at the root, where nothing answers, so the container showed as unhealthy even though Bindery was running. It now asks under the prefix. Thanks to manolb for the report.
