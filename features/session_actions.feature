Feature: Session actions - rename, resume, delete
  As a c9s user
  I want to rename, resume and delete sessions
  So that I can manage my history without losing anything by accident

  Scenario: Confirming delete removes the session file and row
    Given a real session file on disk
    And the active view is the Session View with that session selected
    When I press "d"
    And I press "y"
    Then the session file no longer exists on disk
    And the session no longer appears in the model

  Scenario: Declining delete keeps the session
    Given a real session file on disk
    And the active view is the Session View with that session selected
    When I press "d"
    And I press "n"
    Then the session file still exists on disk
    And the session still appears in the model

  Scenario: Renaming a session sets a custom display label without touching the file
    Given a real session file on disk
    And the active view is the Session View with that session selected
    When I press "n"
    And I type "My renamed session"
    And I press "enter"
    Then the session has the custom label "My renamed session"
    And the session file is unchanged on disk

  Scenario: Cancelling a rename keeps no custom label
    Given a real session file on disk
    And the active view is the Session View with that session selected
    When I press "n"
    And I type "Should not stick"
    And I press "esc"
    Then the session has no custom label

  Scenario: An empty rename clears any existing custom label
    Given a real session file on disk
    And the active view is the Session View with that session selected
    And the session already has the custom label "Old label"
    When I press "n"
    And I clear the rename input
    And I press "enter"
    Then the session has no custom label

  Scenario: Resuming targets the real claude binary when the context alias is only a shell alias
    Given a context whose alias is not a real executable
    And a fake "claude" executable on PATH
    When c9s builds the resume command for that context and session
    Then the resume command runs the fake "claude" executable
    And the resume environment includes a CLAUDE_CONFIG_DIR entry for the context's base dir

  Scenario: Resuming fails with a clear status message when nothing is found on PATH
    Given a real session file on disk
    And the active view is the Session View with that session selected
    And the current context's alias is not a real executable
    And no "claude" executable exists on PATH
    When I press "r"
    Then the status message mentions that no executable was found
