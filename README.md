# Agenda Clubber Lite

A lightweight, self-hosted shared calendar where collectives and producers schedule events and check date clashes before announcing them.

- Backend: Go + Gin; data access through database-agnostic interfaces (`internal/domain`) with a MongoDB implementation (single-node replica set)
- Frontend: server-rendered `html/template`, HTMX, Alpine.js (CSP build), vanilla CSS
- Visual identity: ported from CircuitoNE
- Runs under Podman; production at `agenda.magalz.space`

UI text is in Brazilian Portuguese; code, identifiers and commits are in English.

Setup, configuration and deployment docs will be added as the project takes shape.

---

Made by IgnisDev · ignisdev@magalz.space
