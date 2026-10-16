### Fixed
- **The first import is now recorded** (#3305). The setup funnel looked for an event the importer never writes, so the checklist never saw an import and `Complete` stayed false. Installs with history pick it up from their real first import on the next telemetry ping.
