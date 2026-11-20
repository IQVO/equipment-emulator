---
id: index
title: Architecture decision records
sidebar_label: ADR index
slug: /
sidebar_position: 0
---

# Architecture decision records

The decisions of `equipment-emulator`, in the order they were taken. The ADR bodies are
immutable; a changed decision gets a new ADR that supersedes the old one. The
*Status* column is copied from each ADR's own status line.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](/docs/adr/0001-emulator-role-and-boundaries) | The emulator is test infrastructure at ISA-95 L1/L0, not a bounded context | Accepted (2026-11-20). Design source: warehouse-docs `docs/wcs/implementation-handoff.md`, "Emulator scope". |
| [0002](/docs/adr/0002-vda5050-v3-robot-side-subset) | VDA 5050 v3 robot side, the implemented subset and its known gap | Accepted (2026-11-20). Builds on [ADR 0001](/docs/adr/0001-emulator-role-and-boundaries). |
| [0003](/docs/adr/0003-asrs-telegram-protocol-v1) | AS/RS telegram protocol v1, the PLC model and the handshake | Accepted (2026-11-20). Companion: IQVO/asrs-control ADR 0002 (its conformance). |
| [0004](/docs/adr/0004-simulated-time-fault-injection-and-admin-api) | Simulated time, fault injection and an unauthenticated admin REST API | Accepted (2026-11-20). Builds on [ADR 0001](/docs/adr/0001-emulator-role-and-boundaries) and [ADR 0003](/docs/adr/0003-asrs-telegram-protocol-v1). |
| [0005](/docs/adr/0005-opentelemetry-traces-and-metrics) | OpenTelemetry traces and metrics, per the fleet observability standard | Accepted (2026-11-20). Applies the fleet observability standard (outbound-shipping ADR 0008, inventory-storage ADR 0016) to the emulator; the telemetry package is the customer-returns / outbound-shipping shape. Builds on [ADR 0001](/docs/adr/0001-emulator-role-and-boundaries) and [ADR 0004](/docs/adr/0004-simulated-time-fault-injection-and-admin-api). |
| [0006](/docs/adr/0006-rest-guideline-on-the-admin-api) | The fleet REST guideline on the admin API (optional Idempotency-Key, keyset paging) | Accepted (2026-11-20). Applies the fleet REST API guideline (warehouse-docs `docs/standards/rest-api-guideline.md`, rules R1-R9) and the idempotency replay classes (warehouse-harness-template `docs/idempotency-replay-classes.md`) to the admin API of [ADR 0004](/docs/adr/0004-simulated-time-fault-injection-and-admin-api). Two deviations from the fleet are recorded here: the key is optional, and the store is in memory. |
