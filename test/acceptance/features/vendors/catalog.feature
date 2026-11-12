@postgres
Feature: Version Catalog
  The catalogue lists the Terraform and OpenTofu versions a stack may use,
  newest first: the stable releases each vendor's release index lists,
  which the vendors.refresh periodic task reads into the database once a
  period across every instance, so no request reads an index. A version
  constraint resolves to the newest listed version it allows; constraints
  that do not parse, or that no listed version meets, answer 422 naming the
  constraints parameter. An index that stops answering keeps its vendor's
  versions already listed, and is logged. Any authenticated caller reads the catalogue. Here the
  indexes are a test server serving the fixtures recorded from the real
  ones, Terraform's at first without its newest release.

  Background:
    Given the release indexes serve the recorded fixtures, Terraform's without its newest release

  @op:terraformVersions @op:openTofuVersions @case:success
  Scenario: The first refresh lists each index's stable releases, newest first, reading each index once between the instances
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    Then token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first, without the newest
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And each release index was read once
    And token "m" reads the Terraform versions through B in pages of 7 as the fixture's stable releases newest first, without the newest
    And token "m" reads the OpenTofu versions through A in pages of 2 as the fixture's stable releases newest first

  @op:terraformVersions @case:success
  Scenario: A later refresh adds exactly the release the index has since published
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first, without the newest
    When the Terraform index publishes its newest release
    And the "vendors.refresh" periodic task falls due
    Then "vendors.refresh" eventually runs once more, as one job
    And token "h" reads the Terraform versions through B in pages of 10 as the fixture's stable releases newest first

  @op:terraformVersions @op:openTofuVersions @case:failure
  Scenario: An index that stops answering keeps the versions already listed
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first, without the newest
    When the Terraform index stops answering
    And the Terraform index publishes its newest release
    And the "vendors.refresh" periodic task falls due
    Then "vendors.refresh" eventually runs once more, as one job
    And the instances logged "release index unreadable; the stored releases stand" once more between them
    And token "h" reads the OpenTofu versions through B in pages of 10 as the fixture's stable releases newest first
    And token "h" reads the Terraform versions through B in pages of 10 as the fixture's stable releases newest first, without the newest
    When token "h" sends "GET /api/v1/terraform-versions?cursor=nope" through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/open-tofu-versions?cursor=nope" through B
    Then it answers a 400 problem "invalid-cursor"

  @op:terraformEffectiveVersion @op:openTofuEffectiveVersion @case:success
  Scenario: A constraint resolves to the newest listed version it allows
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first, without the newest
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    When token "m" resolves the Terraform constraints ">= 1.5.0, < 1.6.0" through A
    Then the answer is the version "1.5.7"
    When token "m" resolves the Terraform constraints "~> 1.6.0" through B
    Then the answer is the version "1.6.6"
    When token "h" resolves the OpenTofu constraints "< 1.6.1" through B
    Then the answer is the version "1.6.0"
    When token "h" resolves the OpenTofu constraints "~> 1.6.0" through A
    Then the answer is the version "1.6.3"

  @op:terraformEffectiveVersion @op:openTofuEffectiveVersion @case:failure
  Scenario: Constraints that do not parse, or that no listed version meets, answer 422 naming the parameter
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first, without the newest
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    When token "h" resolves the Terraform constraints "latest" through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "#/query/constraints" as "format"
    When token "h" resolves the Terraform constraints "> 999.0.0" through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "#/query/constraints" as "rule"
    When token "h" resolves the OpenTofu constraints "~> 1.6.0, > 1.7" through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "#/query/constraints" as "rule"
    When token "h" resolves the OpenTofu constraints "" through B
    Then it answers a 422 problem "validation-failed"

  @op:terraformVersions @op:openTofuVersions @op:terraformEffectiveVersion @op:openTofuEffectiveVersion @case:denial
  Scenario: With the catalogue switched off for the installation, each operation answers 403
    Given the installation switches off the features "version-catalog"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "GET /api/v1/terraform-versions" through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /api/v1/open-tofu-versions" through B
    Then it answers a 403 problem "feature-disabled"
    When token "h" resolves the Terraform constraints "1.5.7" through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" resolves the OpenTofu constraints "1.6.0" through B
    Then it answers a 403 problem "feature-disabled"

  @op:terraformVersions @op:openTofuVersions @op:terraformEffectiveVersion @op:openTofuEffectiveVersion @case:denial
  Scenario: A caller with no credential reads nothing of the catalogue
    Given two infraweave servers, A and B, on one database
    When a caller with no token sends "GET /api/v1/terraform-versions" through A
    Then it answers a 401 problem "unauthenticated"
    When a caller with no token sends "GET /api/v1/open-tofu-versions" through B
    Then it answers a 401 problem "unauthenticated"
    When a caller with no token sends "GET /api/v1/terraform-effective-version?constraints=1.5.7" through A
    Then it answers a 401 problem "unauthenticated"
    When a caller with no token sends "GET /api/v1/open-tofu-effective-version?constraints=1.6.0" through B
    Then it answers a 401 problem "unauthenticated"
