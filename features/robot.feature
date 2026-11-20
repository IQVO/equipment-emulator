Feature: The emulated robot reproduces the WCS failure path
  Scenario: A blocked robot stops and reports it
    Given a robot "R1" at x 0
    And an order "o1" from x 0 to x 10
    When robot "R1" is blocked
    And 5 seconds pass
    Then robot "R1" is at x 0
    And robot "R1" reports error "ROBOT_BLOCKED"

  Scenario: An unblocked robot finishes its order
    Given a robot "R1" at x 0
    And an order "o1" from x 0 to x 10
    When robot "R1" is blocked
    And 5 seconds pass
    And robot "R1" is unblocked
    And 10 seconds pass
    Then robot "R1" is at x 10
