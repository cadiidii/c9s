Feature: TUI navigation between contexts, projects, sessions and queries
  As a c9s user
  I want keyboard-driven drill-down navigation
  So that I can get from a context down to an individual query without a mouse

  Background:
    Given a loaded model with 1 context "work" and 1 project containing 2 sessions

  Scenario: Switching context via Enter in the Context View
    Given the active view is the Context View
    When I press "enter" on the "work" context row
    Then the active context becomes "work"
    And the active view becomes the Project View

  Scenario: Drilling from the Project View into the Session View
    Given the active view is the Project View
    When I press "enter" on the first project row
    Then the active view becomes the Session View
    And the sessions shown belong to that project

  Scenario: Drilling from the Session View into the Query View
    Given the active view is the Session View
    When I press "enter" on the first session row
    Then the active view becomes the Query View

  Scenario: Escaping the Query View returns to the Session View
    Given the active view is the Query View
    When I press "esc"
    Then the active view becomes the Session View

  Scenario: Filtering narrows the visible project rows
    Given the active view is the Project View
    And projects include "enrichment-config" and "coreplatform"
    When I filter by "enrichment"
    Then only "enrichment-config" is visible

  Scenario: An active filter takes priority over leaving the Query View on Esc
    Given the active view is the Query View
    And I am filtering by "abc"
    When I press "esc"
    Then the filter is cleared
    And the active view remains the Query View
