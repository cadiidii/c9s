Feature: Session and project history parsing
  As a c9s user
  I want session transcripts scanned into accurate summaries
  So that I can see real usage instead of placeholder data

  Scenario: A session file yields prompt, exchange, token and cost totals
    Given a session transcript with:
      | type      | role      | content     | isMeta |
      | user      | user      | Fix the bug | false  |
      | assistant | assistant |             | false  |
    And the transcript has a final cost-state of $1.50 across model "claude-sonnet-5" with 1000 tokens
    When I parse the session
    Then the session last prompt is "Fix the bug"
    And the session has 2 exchanges
    And the session cost is $1.50
    And the session tokens are 1000

  Scenario: The last prompt is the most recent real user prompt, not the first
    Given a session transcript with:
      | type      | role      | content        | isMeta |
      | user      | user      | First question | false  |
      | assistant | assistant |                | false  |
      | user      | user      | Latest ask     | false  |
      | assistant | assistant |                | false  |
      | user      | user      | caveat noise   | true   |
    When I parse the session
    Then the session last prompt is "Latest ask"

  Scenario: A session renamed in Claude Code carries that name, the latest rename winning
    Given a session transcript with:
      | type | role | content | isMeta |
      | user | user | Hello   | false  |
    And the transcript was renamed in Claude Code to "Auth refactor"
    And the transcript was later renamed in Claude Code to "Auth refactor v2"
    When I parse the session
    Then the session name is "Auth refactor v2"

  Scenario: A session never renamed gets the title Claude Code generated for it
    Given a session transcript with:
      | type | role | content | isMeta |
      | user | user | Hello   | false  |
    And the transcript has a generated title "Fixing login bug"
    When I parse the session
    Then the session name is "Fixing login bug"

  Scenario: A rename in Claude Code beats the generated title
    Given a session transcript with:
      | type | role | content | isMeta |
      | user | user | Hello   | false  |
    And the transcript has a generated title "Fixing login bug"
    And the transcript was renamed in Claude Code to "Auth refactor"
    When I parse the session
    Then the session name is "Auth refactor"

  Scenario: A session with no title at all has no name
    Given a session transcript with:
      | type | role | content | isMeta |
      | user | user | Hello   | false  |
    When I parse the session
    Then the session name is ""

  Scenario: Meta lines are not counted as exchanges
    Given a session transcript with:
      | type | role | content              | isMeta |
      | user | user | local command caveat | true   |
    When I parse the session
    Then the session has 0 exchanges
    And the session last prompt is ""

  Scenario: Malformed lines are skipped without failing the parse
    Given a session transcript containing one valid user line and one line of garbage text
    When I parse the session
    Then parsing the session succeeds
    And the session has 1 exchange

  Scenario: Scanning a context with no history yet
    Given a base directory with no "projects" subdirectory
    When I scan projects for that base directory
    Then scanning succeeds
    And 0 projects are found

  Scenario: Multiple sessions in one project directory aggregate correctly
    Given a project directory containing 2 session files costing $1.00 and $2.00
    When I scan projects for that base directory
    Then 1 project is found
    And the project total cost is $3.00
    And the project has 2 sessions

  Scenario: Monthly totals only include sessions active in the given month
    Given a project with one session last active this month costing $5.00
    And a project with one session last active last month costing $9.00
    When I compute monthly totals for this month
    Then the monthly cost is $5.00
