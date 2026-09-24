Feature: Per-query cost breakdown
  As a c9s user
  I want to see an estimated cost for each individual query in a session
  So that I can see which prompts were expensive without a hardcoded price table

  Scenario: Per-query costs reconcile exactly to the session total
    Given a session with 3 user turns each followed by one assistant reply
    And a final cost-state totalling $9.00 across model "claude-sonnet-5"
    When I compute per-query costs for the session
    Then the sum of all per-query costs equals $9.00

  Scenario: A query with no assistant usage costs nothing
    Given a session with a user turn that has no assistant reply
    When I compute per-query costs for the session
    Then that query's cost is $0.00

  Scenario: Turns are attributed to the correct query group
    Given a session with 2 user turns, the first followed by 2 assistant replies and the second by 1
    When I compute per-query costs for the session
    Then the first query has 2 attributed replies
    And the second query has 1 attributed reply

  Scenario: Multiple models in one session get independent effective rates
    Given a final cost-state with model "claude-haiku-4-5-20251001" costing $0.10 for 100 tokens
    And a final cost-state with model "claude-opus-5-5" costing $9.00 for 100 tokens
    And one query used only the haiku model for 100 tokens
    And another query used only the opus model for 100 tokens
    When I compute per-query costs for the session
    Then the haiku query costs approximately $0.10
    And the opus query costs approximately $9.00
