Feature: The emulated AS/RS reproduces the WCS failure path
  Scenario: A no-read hides the container at the output port
    Given an AS/RS "ASRS1" with container "C1" at level 1 slot 1
    And a no-read at port "OUT1"
    When job "j1" retrieves "C1" from level 1 slot 1 to port "OUT1"
    And 30 seconds pass
    Then the AS/RS reports a port arrival at "OUT1" without a container

  Scenario: A unit fault holds the job until it clears
    Given an AS/RS "ASRS1" with container "C1" at level 1 slot 1
    When job "j1" retrieves "C1" from level 1 slot 1 to port "OUT1"
    And aisle 1 faults
    And 60 seconds pass
    Then job "j1" is not done
    When aisle 1 recovers
    And 30 seconds pass
    Then job "j1" is done
