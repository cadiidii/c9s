Feature: Session actions - delete
  As a c9s user
  I want to delete a session transcript with confirmation
  So that I don't lose history by accident

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
