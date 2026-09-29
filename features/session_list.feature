Feature: Session View columns
  As a c9s user
  I want each session row to show its name and its last prompt
  So that I can tell sessions apart at a glance

  Scenario: A renamed session shows its name and its last prompt
    Given a session list with a session named "Auth refactor" whose last prompt is "add retry to login"
    When I open the Session View
    Then the header has a NAME column and a LAST PROMPT column
    And the row for that session shows "Auth refactor" and "add retry to login"

  Scenario: An unnamed session shows a dash for its name
    Given a session list with an unnamed session whose last prompt is "fix flaky test"
    When I open the Session View
    Then the row for that session shows "-" as its name and "fix flaky test"

  Scenario: Filtering matches the session name
    Given a session list with a session named "Auth refactor" whose last prompt is "add retry to login"
    And a session list also containing an unnamed session whose last prompt is "fix flaky test"
    When I filter the sessions by "auth"
    Then only the session named "Auth refactor" is listed
