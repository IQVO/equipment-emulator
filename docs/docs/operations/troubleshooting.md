---
id: troubleshooting
title: Troubleshooting
sidebar_position: 4
description: "Symptoms seen from fleet control or asrs-control, and their causes."
---

# Troubleshooting

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| A robot never reacts to an order and `state` shows no error | the order had an edge action or a node without `nodePosition`: dropped without feedback | look for `unsupported order` in the log; remove edge actions, give every node a position |
| A robot's `state` carries `INVALID_ORDER` | a new order while the previous one is not finished, or its first node is neither `lastNodeId` nor within 0.5 m | wait for the order to finish, or start at the robot's position |
| `OUTDATED_ORDER_UPDATE` / `SAME_ORDER_UPDATE_ID` | an update with a lower or equal `orderUpdateId` | increase `orderUpdateId` |
| A robot publishes nothing and ignores orders | a `ROBOT_DISCONNECT` fault is on | `POST /faults` with `"active": false` |
| A robot stopped with `ROBOT_BLOCKED` or `BATTERY_EMPTY` | an injected obstacle, or the state of charge reached 0 | clear the fault; `ROBOT_BATTERY` sets the charge |
| A telegram is ACKed `NOT_READY` | the emulator is still starting | retransmit; it is not remembered for deduplication |
| `DUPLICATE_JOB` for a new job | the `jobId` was used before in this process lifetime, even if that job ended | use a fresh `jobId`, or restart the emulator |
| A RETRIEVE's `PORT_ARRIVAL` has no `containerId` | an `ASRS_NO_READ` fault on the output port | clear the fault |
| Jobs of one aisle stop running | an `ASRS_UNIT` fault holds the aisle unit (`HELD`) | clear the fault; the queued jobs continue |
| Timestamps run ahead of the wall clock | `SIM_SPEED` above 1: timestamps are simulated time | compare message timestamps, not wall time |
| Pod not ready | the broker at `MQTT_URL` is unreachable | check the Mosquitto Service and `mqtt.url` |
