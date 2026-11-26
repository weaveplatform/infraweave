@postgres
Feature: Feature previews when account settings are disabled
  The installation's account-settings switch refuses the preview read and
  both settings, including an account admin's requests.
  ref: decision 0021

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @op:featurePreviewSetPersonalEnabled @case:denial
  Scenario: Disabling account settings refuses all feature preview operations
    Given a running infraweave server whose features.disabled is "account-settings"
    And the home key is exchanged through A for token "admin"
    When token "admin" sends "GET /api/v1/feature-preview" through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "account-settings"
    When token "admin" sends "POST /api/v1/account/set-feature-previews-enabled" with '{"enabled":true}' through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "account-settings"
    When token "admin" sends "PUT /api/v1/feature-preview/personal" with '{"enabled":false}' through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "account-settings"
