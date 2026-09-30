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

  Scenario: The all-sessions list shows which project each session belongs to
    Given two projects where "/work/alpha" has a session and "/work/beta" has a session
    When I open the all-sessions list
    Then the header has a PROJECT column
    And a row shows "/work/alpha" and a row shows "/work/beta"

  Scenario: A single project's session list has no PROJECT column
    Given two projects where "/work/alpha" has a session and "/work/beta" has a session
    When I open the sessions of "/work/alpha"
    Then the header has no PROJECT column

  Scenario: Filtering the all-sessions list matches the project path
    Given two projects where "/work/alpha" has a session and "/work/beta" has a session
    When I open the all-sessions list
    And I filter the sessions by "beta"
    Then only the session from "/work/beta" is listed
