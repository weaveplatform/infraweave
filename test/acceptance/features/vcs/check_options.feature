@postgres @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Aggregate check options follow the feature catalogue
  Every served provider create, replace and patch refuses gated aggregate modes
  before writing an integration. Default individual checks remain available.
  ref: decisions 0012 D12, 0016, 0021 and 0022

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And the operator key is exchanged through A for token "op"

  @case:denial
  Scenario Outline: Billing denies aggregate modes on every provider write
    Given a "<provider>" check integration using individual mode
    And token "op" turns billing on through A
    And token "op" gives the home organization a seeded tier with "vcs-integrations" lacking "aggregated-vcs-checks" through A
    When the check integration requests "POST" with mode "<mode>"
    Then it answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "aggregated-vcs-checks"
    And the problem lists the catalogue tiers holding "aggregated-vcs-checks"
    And the refused check mode leaves the integration unchanged
    When the check integration requests "PUT" with mode "<mode>"
    Then it answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "aggregated-vcs-checks"
    And the problem lists the catalogue tiers holding "aggregated-vcs-checks"
    And the refused check mode leaves the integration unchanged
    When the check integration requests "PATCH" with mode "<mode>"
    Then it answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "aggregated-vcs-checks"
    And the problem lists the catalogue tiers holding "aggregated-vcs-checks"
    And the refused check mode leaves the integration unchanged

    @op:githubEnterpriseIntegrationCreate @op:githubEnterpriseIntegrationUpdate @op:githubEnterpriseIntegrationPatch
    Examples:
      | provider | mode |
      | GitHub | AGGREGATED |
      | GitHub | ALL |

    @op:gitlabIntegrationCreate @op:gitlabIntegrationUpdate @op:gitlabIntegrationPatch
    Examples:
      | provider | mode |
      | GitLab | AGGREGATED |
      | GitLab | ALL |

    @op:bitbucketCloudIntegrationCreateV2 @op:bitbucketCloudIntegrationUpdateV2 @op:bitbucketCloudIntegrationPatch
    Examples:
      | provider | mode |
      | Bitbucket Cloud | AGGREGATED |
      | Bitbucket Cloud | ALL |

    @op:bitbucketDatacenterIntegrationCreate @op:bitbucketDatacenterIntegrationUpdate @op:bitbucketDatacenterIntegrationPatch
    Examples:
      | provider | mode |
      | Bitbucket DC | AGGREGATED |
      | Bitbucket DC | ALL |

    @op:azureDevOpsRepoIntegrationCreate @op:azureDevOpsRepoIntegrationUpdate @op:azureDevOpsRepoIntegrationPatch
    Examples:
      | provider | mode |
      | Azure DevOps | AGGREGATED |
      | Azure DevOps | ALL |

  @case:denial
  Scenario Outline: Operator-disabled aggregate modes refuse every provider write
    Given a "<provider>" check integration using individual mode
    When the check integration requests "POST" with mode "<mode>"
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "aggregated-vcs-checks"
    And the refused check mode leaves the integration unchanged
    When the check integration requests "PUT" with mode "<mode>"
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "aggregated-vcs-checks"
    And the refused check mode leaves the integration unchanged
    When the check integration requests "PATCH" with mode "<mode>"
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "aggregated-vcs-checks"
    And the refused check mode leaves the integration unchanged

    @op:githubEnterpriseIntegrationCreate @op:githubEnterpriseIntegrationUpdate @op:githubEnterpriseIntegrationPatch
    Examples:
      | provider | mode |
      | GitHub | AGGREGATED |
      | GitHub | ALL |

    @op:gitlabIntegrationCreate @op:gitlabIntegrationUpdate @op:gitlabIntegrationPatch
    Examples:
      | provider | mode |
      | GitLab | AGGREGATED |
      | GitLab | ALL |

    @op:bitbucketCloudIntegrationCreateV2 @op:bitbucketCloudIntegrationUpdateV2 @op:bitbucketCloudIntegrationPatch
    Examples:
      | provider | mode |
      | Bitbucket Cloud | AGGREGATED |
      | Bitbucket Cloud | ALL |

    @op:bitbucketDatacenterIntegrationCreate @op:bitbucketDatacenterIntegrationUpdate @op:bitbucketDatacenterIntegrationPatch
    Examples:
      | provider | mode |
      | Bitbucket DC | AGGREGATED |
      | Bitbucket DC | ALL |

    @op:azureDevOpsRepoIntegrationCreate @op:azureDevOpsRepoIntegrationUpdate @op:azureDevOpsRepoIntegrationPatch
    Examples:
      | provider | mode |
      | Azure DevOps | AGGREGATED |
      | Azure DevOps | ALL |
