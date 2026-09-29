Feature: Full-screen layout
  As a c9s user
  I want c9s to fill the terminal like k9s does
  So that I see as many rows as fit and nothing wraps or overflows

  Scenario: The view fills exactly the terminal height and never exceeds its width
    Given a terminal of 100 columns by 30 rows
    And a Session View with 5 sessions
    When the screen is rendered
    Then the output is exactly 30 lines tall
    And no line is wider than 100 columns
    And the footer legend is on the last line

  Scenario: A long list scrolls to keep the selected row visible
    Given a terminal of 100 columns by 20 rows
    And a Session View with 100 sessions
    When I press "j" 60 times
    Then the selected row is visible on screen
    And the output is exactly 20 lines tall

  Scenario: Text columns use the extra width of a wide terminal
    Given a session whose last prompt is 120 characters long
    When it is rendered in a terminal 80 columns wide
    And it is rendered in a terminal 220 columns wide
    Then the 80 column rendering truncates the prompt
    And the 220 column rendering shows the whole prompt

  Scenario: Shrinking the terminal re-flows the view
    Given a terminal of 100 columns by 30 rows
    And a Session View with 5 sessions
    When the terminal is resized to 60 columns by 15 rows
    Then the output is exactly 15 lines tall
    And no line is wider than 60 columns

  Scenario: c9s runs on the alternate screen so it is restored cleanly after claude exits
    When c9s runs and then quits
    Then it switches the terminal to the alternate screen
    And it switches the terminal back to the normal screen on exit
