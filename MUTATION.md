# Mutation testing: residual mutants

Gate: `.gremlins.yaml` (`efficacy: 99`, `mutant-coverage: 99`; gremlins fails when the measured
value is <= the threshold, so every package must measure above 99). `make mutation` and the
`mutation-fast` CI job run `gremlins unleash` once per domain package (`./internal/domain/*/`);
`make mutation-full` and the weekly `mutation` job run the aggregate `./internal/domain`.

Measured 2026-11-20 (gremlins v0.6.0, `workers: 1`, `timeout-coefficient: 30`):

| Package | Killed | Lived | Not covered | Timed out | Efficacy | Mutator coverage |
|---|---|---|---|---|---|---|
| asrs | 66 | 0 | 0 | 1 | 100.00% | 100.00% |
| robot | 109 | 1 | 0 | 0 | 99.09% | 100.00% |
| simclock | 1 | 0 | 0 | 0 | 100.00% | 100.00% |

Aggregate (`make mutation-full`): 176 killed, 1 lived, 0 not covered, 1 timed out, efficacy 99.44%,
mutator coverage 100.00%.

Before this ratchet (origin/develop at aa4ee6e, gate 85/96): asrs 77.94% / 100%, robot 94.59% /
97.37%, simclock 60.00% / 100%; aggregate 161 killed, 23 lived, 3 not covered, efficacy 87.50%,
mutator coverage 98.40%.

## Mutants that cannot be killed

| Package | File:line | Mutator | Why |
|---|---|---|---|
| robot | robot.go:90:25 (`drive`) | CONDITIONALS_BOUNDARY (`DrainPerMeter > 0` to `>= 0`) | Equivalent. The mutant only differs at `DrainPerMeter == 0`, where it computes `reach := soc / 0`. `drive` only runs with `soc > 0` (`Tick` returns at `soc <= 0`, and with zero drain the charge never falls mid-tick), so `reach` is `+Inf` and `reach < d` is false: not capped, exactly as without the branch. Even with `soc == 0` forced, `0/0` is NaN and `NaN < d` is false. The guard stays: it is what keeps a negative drain from producing a negative reach. |

## Timed out (counted by neither efficacy nor coverage)

| Package | File:line | Mutator | Note |
|---|---|---|---|
| asrs | system.go:44:30 (`New`) | INCREMENT_DECREMENT (`a++` to `a--`) | The aisle loop never ends and appends units without bound; the test binary times out. Both CI mutation jobs run under `ulimit -v 4000000`, so on Linux the mutant dies with "out of memory" instead of exhausting the runner VM. |

## Rewritten so an equivalent mutant cannot exist

Behaviour-preserving rewrites (own commit, existing tests unchanged) of spots whose boundary
mutant no input could tell apart:

| Package | Where | Before | After | Why the old mutant was equivalent |
|---|---|---|---|---|
| robot | robot.go `drive` | `MaxSpeed > 0 && MaxSpeed < speed { speed = MaxSpeed }` | `MaxSpeed > 0 { speed = math.Min(speed, MaxSpeed) }` | At `MaxSpeed == speed` the `<=` mutant assigned the same value. |
| robot | view.go `sortedStates` | `sort.Slice(..., a.ID < b.ID)` | `slices.SortFunc(..., strings.Compare(a.ID, b.ID))` | IDs are unique map keys, so `<` and `<=` sort alike. |
| asrs | view.go `Snapshot` | `sort.Slice(..., a.ID < b.ID)` | `slices.SortFunc(..., strings.Compare(a.ID, b.ID))` | Unit IDs are unique (one per aisle). |
| simclock | clock.go `Advance`, `Scale` | `if d > 0 {...}`, `if speed <= 0 { return 0 }` | `Add(max(d, 0))`, `real * max(speed, 0)` | At `d == 0` / `speed == 0` the guarded and unguarded paths return the same value. |

The tagless `switch` on the order-update ID in `robot/order.go` (`AcceptOrder`) became an if-chain:
Go's cover tool does not instrument `case` expressions, so its three mutants were NOT COVERED although
both arms were tested. They are now covered and killed.
