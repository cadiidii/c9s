Feature: Context configuration loading
  As a user with multiple Claude Code profiles
  I want c9s to load my context configuration safely
  So that a missing or malformed config never crashes the app

  Scenario: No config file exists yet
    Given no config file exists at "~/.config/c9s/config.yaml"
    When I load the configuration
    Then the current context is "work"
    And the "work" context has alias "claude-work"
    And the "personal" context has alias "claude-personal"

  Scenario: A valid custom config file exists
    Given a config file with:
      """
      current-context: personal
      contexts:
        personal:
          alias: claude-personal
          base_dir: ~/.claude-personal
      """
    When I load the configuration
    Then the current context is "personal"
    And the "personal" context has base dir "~/.claude-personal"

  Scenario: A malformed config file falls back to defaults
    Given a config file with:
      """
      current-context: [this is not valid yaml
      """
    When I load the configuration
    Then loading the configuration returns an error
    And the current context is "work"

  Scenario: An env-indirected API key resolves from the process environment
    Given a config file with:
      """
      current-context: work
      contexts:
        work:
          alias: claude-work
          base_dir: ~/.claude-work
          env:
            ANTHROPIC_API_KEY: "env:WORK_API_KEY"
      """
    And the environment variable "WORK_API_KEY" is set to "secret-123"
    When I resolve the environment for the "work" context
    Then the resolved environment includes "ANTHROPIC_API_KEY=secret-123"

  Scenario: Base dir tilde expands to the home directory
    When I resolve the base dir "~/.claude-work"
    Then the resolved base dir equals my home directory joined with ".claude-work"
