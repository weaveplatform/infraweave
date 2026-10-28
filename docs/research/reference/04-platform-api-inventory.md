# 04: Platform API inventory

Checked against: docs read 2026-10-28; schema `b3255fe38f5e`; spacelift-api-bruno `7cad4574954b`; terraform-provider-spacelift v1.57.0; the specification reference `api/reference/spacelift/spacelift.openapi.yaml` generated from them.

Research baseline: **2026-10-28**.

This report lists every Spacelift operation outside the deployment core of
report 03: identity and access, account settings and billing, source control,
cloud and storage integrations, events and notifications, the registries and
version catalog, drift detection, resources and scans, templates and
blueprints, Intelligence and Intent, Flows, plugins, the web UI's own
endpoints, and the eight operations the reference leaves untagged. It uses
the table layout and enrichment rules of [report 03, section 1](03-deploy-core-api-inventory.md#1-how-to-read-the-tables):
every row is generated from the specification reference, then enriched with
permissions, tier gates, side effects and deprecations. Reports 11 to 25
hold the behaviour behind these tags.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[B]** | spacelift-api-bruno at `7cad4574954b` |
| **[I]** | Inferred; the cell says from what |

## 1. Operations in this report

Every tag of the reference not covered by report 03 appears here, in the
section named after it. The counts are generated:

| Tag | Sub-tag | Operations |
|---|---|---|
| AI Integrations | (top level) | 5 |
| API Keys | (top level) | 7 |
| API Keys | Fields of `ApiKey` | 1 |
| Account | (top level) | 7 |
| Account Settings | (top level) | 42 |
| Ansible | (top level) | 5 |
| Ansible | Fields of `AnsibleHost` | 2 |
| Audit Trail | (top level) | 6 |
| Auth | (top level) | 6 |
| Billing | (top level) | 14 |
| Blob Storage | (top level) | 4 |
| Blueprints | (top level) | 10 |
| Blueprints | Deployments | 2 |
| Blueprints | Groups | 3 |
| Cloud Integrations | AWS | 14 |
| Cloud Integrations | Azure | 10 |
| Cloud Integrations | Fields of `AwsIntegration` | 2 |
| Cloud Integrations | Fields of `AzureIntegration` | 1 |
| Debug | (top level) | 1 |
| Drift Detection | (top level) | 3 |
| External Integrations | (top level) | 4 |
| Feature Flags | (top level) | 1 |
| Flows | (top level) | 5 |
| Flows | Root fields with no Bruno request | 1 |
| Forms | (top level) | 3 |
| GPG Keys | (top level) | 4 |
| Infrastructure | (top level) | 3 |
| Intent | (top level) | 33 |
| Intent | Fields of `IntentChatConversation` | 1 |
| Intent | Fields of `IntentProject` | 6 |
| Managed Users | (top level) | 8 |
| Managed Users | Fields of `ManagedUserGroup` | 2 |
| Managed Users | Fields of `ManagedUser` | 2 |
| Managed Users | User Groups | 11 |
| Notifications | (top level) | 5 |
| OAuth | (top level) | 1 |
| OpenTofu Migration | (top level) | 4 |
| Origin Integration | (top level) | 4 |
| Personal API Keys | (top level) | 4 |
| Plugins | (top level) | 5 |
| Plugins | Templates | 4 |
| Repos | (top level) | 11 |
| Repos | Fields of `Repo` | 1 |
| Resources | (top level) | 4 |
| Roles | (top level) | 5 |
| Roles | API Key Role Bindings | 5 |
| Roles | Stack Role Bindings | 5 |
| Roles | User Group Role Bindings | 5 |
| Roles | User Role Bindings | 5 |
| SSO | (top level) | 11 |
| Saved Filters | (top level) | 5 |
| Scans | (top level) | 11 |
| Scans | Fields of `ScanRun` | 4 |
| Scans | Fields of `Scan` | 2 |
| Search Suggestions | (top level) | 30 |
| Sessions and Security Keys | (top level) | 5 |
| Slack | (top level) | 7 |
| Templates | (top level) | 6 |
| Templates | Deployments | 6 |
| Templates | Fields of `TemplateDeployment` | 1 |
| Templates | Fields of `Template` | 5 |
| Templates | Versions | 5 |
| Terraform Providers | (top level) | 16 |
| Terraform Providers | Fields of `TerraformProvider` | 1 |
| Tofu Workspaces | (top level) | 3 |
| UI State | (top level) | 2 |
| User Guide | (top level) | 18 |
| User Guide | Fields of `UserGuideChapter` | 1 |
| User Guide | Fields of `UserGuideGroup` | 1 |
| VCS Agent Pools | (top level) | 6 |
| VCS Integrations | (top level) | 10 |
| VCS Integrations | Azure DevOps | 5 |
| VCS Integrations | Bitbucket Cloud | 7 |
| VCS Integrations | Bitbucket Datacenter | 6 |
| VCS Integrations | Fields of `VCSIntegration` | 2 |
| VCS Integrations | GitHub | 2 |
| VCS Integrations | GitHub Enterprise | 6 |
| VCS Integrations | GitLab | 6 |
| Version Catalog | (top level) | 7 |
| Viewer | (top level) | 1 |
| Webhooks | (top level) | 7 |
| Webhooks | Fields of `NamedWebhooksIntegration` | 2 |
| (untagged) | (top level) | 8 |
| **Total** | | **512** |

The RBAC action catalog and the tier feature table that the Notes column
draws on are sections 9 and 10 of [report 03](03-deploy-core-api-inventory.md#9-rbac-action-catalog).
Most platform operations have no `Action` value **[G]**: the docs tie them to
being a root space admin or an account admin instead
[[D]](https://docs.spacelift.io/concepts/authorization/assigning-roles-users),
and the tables record the restriction only where a description states it.

## 2. Identity and access

### 2.1 Auth

Machine clients exchange an API key's ID and secret for a short-lived JWT
with `apiKeyUser`, served here as the only public route, `POST /auth/token`
**[G]**; report 01 records the token flow. The GitHub OAuth trio
(`oauthRedirect`, `oauthToken`, `oauthUser`) and `cliConfirmationToken` back
interactive logins of the web UI and spacectl **[G]**.

### Auth (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/auth/cli-confirmation-tokens` | `cliConfirmationToken` (M) | Generates a confirmation token for CLI auth. [G] | body `CliConfirmationTokenRequest`; returns `String` [G]; Bruno marks it advanced: part of the spacectl cli login flow [B]; Backs `spacectl profile login` browser flow (report 06) [I] from the name |
| POST | `/auth/github/redirect` | `oauthRedirect` (M) | Get a GitHub URL to redirect to. [G] | returns `OAuthRedirect` [G]; Bruno marks it advanced: part of a browser-based oauth redirect flow; cannot be completed with an api key [B] |
| POST | `/auth/github/token` | `oauthToken` (M) | Exchange code and temporary token for a GitHub token. [G] | body `OauthTokenRequest`; returns `String` [G]; Bruno marks it advanced: part of a browser-based oauth redirect flow; cannot be completed with an api key [B] |
| POST | `/auth/github/user` | `oauthUser` (M) | Exchange GitHub token for a Spacelift User. [G] | body `OauthUserRequest`; returns `User` [G]; Bruno marks it advanced: part of a browser-based oauth redirect flow; cannot be completed with an api key [B]; the docs also use it directly to swap a GitHub personal access token for a JWT, for accounts whose identity provider is GitHub [[D]](https://docs.spacelift.io/integrations/api), and spacectl does the same with `SPACELIFT_API_GITHUB_TOKEN` (report 06); the docs are the higher source |
| POST | `/auth/logout` | `logout` (M) | End the current session. [G] | returns `Session` [G]; Invalidates the current session [B] |
| POST | `/auth/token` | `apiKeyUser` (M) | Exchange an API token for a Spacelift User. [G] | body `ApiKeyUserRequest`; returns `User` [G]; Public: exchanges an API key ID and secret for a JWT (report 01) [G] |

### 2.2 API Keys

Account API keys are subjects with their own role bindings; the secret is
returned once and `apiKeyReset` replaces it **[B]**. OIDC-backed keys
(`ApiKeyInput.oidc`) need the Enterprise plan
[[D]](https://docs.spacelift.io/integrations/api). `apiKeyEnableNonRootManagers`
lets non-root space admins create keys up to their own level **[G]**.

### API Keys (7)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| PUT | `/account/api-key-non-root-managers` | `apiKeyEnableNonRootManagers` (M) | Enable/disable non-root managers for API keys. Settings this to true means that admins of non-root spaces can create an API key up to their current level of permissions. Also, if this is set to true, then the API key creator can delete the API key regardless of their current permissions. [G] | body `ApiKeyEnableNonRootManagersRequest`; returns `Boolean` [G] |
| GET | `/api-keys` | `apiKeys` (Q) | List of all API keys for the account. [G] | returns `[ApiKey]` [G] |
| POST | `/api-keys` | `apiKeyCreate` (M) | Create an API key. [G] | body `ApiKeyInput`; returns `ApiKey` [G]; Tier `API_KEYS_OIDC` [I]; Enterprise, for OIDC keys only [[D]](https://docs.spacelift.io/integrations/api); Secret returned once [B]; `ApiKeyInput.oidc` makes an OIDC key, `admin`, `teams`, `roleBindings` and `expiresAt` set access [G] |
| GET | `/api-keys/{apiKeyId}` | `apiKey` (Q) | Get API Key. [B] | returns `ApiKey` [G] |
| PUT | `/api-keys/{apiKeyId}` | `apiKeyUpdate` (M) | Update an API key. [G] | body `ApiKeyUpdateInput`; returns `ApiKey` [G] |
| DELETE | `/api-keys/{apiKeyId}` | `apiKeyDelete` (M) | Delete an API key. [G] | returns `ApiKey` [G] |
| POST | `/api-keys/{apiKeyId}/reset` | `apiKeyReset` (M) | Reset the secret of an API key. [G] | returns `ApiKey` [G]; Issues a new secret and invalidates the old one [B] |

### API Keys: Fields of `ApiKey` (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/api-keys/{apiKeyId}/api-key-role-bindings` | `apiKeyRoleBindings` on `ApiKey` | List the role bindings of the API key: role, space and key per binding (`ApiKeyRoleBinding`) [G]. | returns `[ApiKeyRoleBinding]` [G] |

### 2.3 Personal API Keys

Keys owned by one user for their own tooling, allowed or not by an account
setting **[B]**. Root admins can list and disable them **[G]**.

### Personal API Keys (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/personal-api-keys` | `personalApiKeys` (Q) | List of all personal API keys for the account with their owners. Root admins only. [G] | returns `[ApiKey]` [G]; Root space admins only [G] |
| POST | `/personal-api-keys` | `personalApiKeyCreate` (M) | Create Personal API Key. [B] | body `PersonalApiKeyInput`; returns `ApiKey` [G] |
| GET | `/personal-api-keys-count` | `personalApiKeysCount` (Q) | Number of personal API keys on the account that are not disabled. Space admins only. [G] | returns `Int` [G]; Space admins only [G] |
| POST | `/personal-api-keys/{personalApiKeyId}/disable` | `personalApiKeyDisable` (M) | Revoke a personal API key. Root admins only. The owner can only delete it afterwards. [G] | returns `ApiKey` [G]; Root space admins only [G] |

### 2.4 Viewer

The subject behind the current token and what it may do **[B]**.

### Viewer (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/viewer` | `viewer` (Q) | Current user, if any. [G] | returns `User` [G]; Bruno marks it advanced: returns the currently authenticated user [B]; Cheapest way to check a token works [B] |

### 2.5 Roles

A role is a named set of `Action` values; a role binding grants it to a user,
an IdP group, an API key or a stack, on a space **[B]**. Root
space admins bind across all spaces; non-root space admins only in spaces
they administer
[[D]](https://docs.spacelift.io/concepts/authorization/assigning-roles-users).
Stack role bindings let a stack act through the Terraform provider
[[D]](https://docs.spacelift.io/concepts/authorization/assigning-roles-stacks).
Report 14 records the model.

### Roles (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/roles` | `roles` (Q) | Returns all roles for the account that are accessible by the requester. [G] | returns `[Role]` [G] |
| POST | `/roles` | `roleCreate` (M) | Create a new Role. [G] | body `RoleInput`; returns `Role` [G]; A role is a set of `Action` values; bindings grant it on a space [[D]](https://docs.spacelift.io/concepts/authorization/rbac-system) |
| GET | `/roles/{roleId}` | `role` (Q) | Get Role. [B] | returns `Role` [G] |
| PUT | `/roles/{roleId}` | `roleUpdate` (M) | Update Role. [B] | body `RoleUpdateInput`; returns `Role` [G] |
| DELETE | `/roles/{roleId}` | `roleDelete` (M) | Delete Role. [B] | returns `Role` [G] |

### Roles: API Key Role Bindings (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/api-key-role-binding-batches` | `apiKeyRoleBindingBatchCreate` (M) | Batch Create API Key Role Bindings. [B] | body `ApiKeyRoleBindingBatchInput`; returns `[ApiKeyRoleBinding]` [G] |
| POST | `/api-key-role-bindings` | `apiKeyRoleBindingCreate` (M) | Create API Key Role Binding. [B] | body `ApiKeyRoleBindingInput`; returns `ApiKeyRoleBinding` [G] |
| GET | `/api-key-role-bindings/{apiKeyRoleBindingId}` | `apiKeyRoleBinding` (Q) | Get API Key Role Binding. [B] | returns `ApiKeyRoleBinding` [G] |
| PUT | `/api-key-role-bindings/{apiKeyRoleBindingId}` | `apiKeyRoleBindingUpdate` (M) | Update API Key Role Binding. [B] | body `ApiKeyRoleBindingInput`; returns `ApiKeyRoleBinding` [G] |
| DELETE | `/api-key-role-bindings/{apiKeyRoleBindingId}` | `apiKeyRoleBindingDelete` (M) | Delete API Key Role Binding. [B] | returns `ApiKeyRoleBinding` [G] |

### Roles: Stack Role Bindings (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/stack-role-binding-batches` | `stackRoleBindingBatchCreate` (M) | Batch Create Stack Role Bindings. [B] | body `StackRoleBindingBatchInput`; returns `[StackRoleBinding]` [G] |
| POST | `/stack-role-bindings` | `stackRoleBindingCreate` (M) | Create Stack Role Binding. [B] | body `StackRoleBindingInput`; returns `StackRoleBinding` [G]; RBAC `STACK_ROLE_BINDING_CREATE` [I] by name, fallback Admin (schema: disabled) [G][D] |
| GET | `/stack-role-bindings/{stackRoleBindingId}` | `stackRoleBinding` (Q) | Get Stack Role Binding. [B] | returns `StackRoleBinding` [G] |
| PUT | `/stack-role-bindings/{stackRoleBindingId}` | `stackRoleBindingUpdate` (M) | Update Stack Role Binding. [B] | body `StackRoleBindingUpdateInput`; returns `StackRoleBinding` [G]; RBAC `STACK_ROLE_BINDING_UPDATE` [I] by name, fallback Admin (schema: disabled) [G][D] |
| DELETE | `/stack-role-bindings/{stackRoleBindingId}` | `stackRoleBindingDelete` (M) | Delete Stack Role Binding. [B] | returns `StackRoleBinding` [G]; RBAC `STACK_ROLE_BINDING_DELETE` [I] by name, fallback Admin (schema: disabled) [G][D] |

### Roles: User Group Role Bindings (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/user-group-role-binding-batches` | `userGroupRoleBindingBatchCreate` (M) | Batch Create User Group Role Bindings. [B] | body `UserGroupRoleBindingBatchInput`; returns `[UserGroupRoleBinding]` [G] |
| POST | `/user-group-role-bindings` | `userGroupRoleBindingCreate` (M) | Create User Group Role Binding. [B] | body `UserGroupRoleBindingInput`; returns `UserGroupRoleBinding` [G] |
| GET | `/user-group-role-bindings/{userGroupRoleBindingId}` | `userGroupRoleBinding` (Q) | Get User Group Role Binding. [B] | returns `UserGroupRoleBinding` [G] |
| PUT | `/user-group-role-bindings/{userGroupRoleBindingId}` | `userGroupRoleBindingUpdate` (M) | Update User Group Role Binding. [B] | body `UserGroupRoleBindingInput`; returns `UserGroupRoleBinding` [G] |
| DELETE | `/user-group-role-bindings/{userGroupRoleBindingId}` | `userGroupRoleBindingDelete` (M) | Delete User Group Role Binding. [B] | returns `UserGroupRoleBinding` [G] |

### Roles: User Role Bindings (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/user-role-binding-batches` | `userRoleBindingBatchCreate` (M) | Batch Create User Role Bindings. [B] | body `UserRoleBindingBatchInput`; returns `[UserRoleBinding]` [G] |
| POST | `/user-role-bindings` | `userRoleBindingCreate` (M) | Create User Role Binding. [B] | body `UserRoleBindingInput`; returns `UserRoleBinding` [G]; Root space admins bind across all spaces; others only in spaces they administer [[D]](https://docs.spacelift.io/concepts/authorization/assigning-roles-users) |
| GET | `/user-role-bindings/{userRoleBindingId}` | `userRoleBinding` (Q) | Get User Role Binding. [B] | returns `UserRoleBinding` [G] |
| PUT | `/user-role-bindings/{userRoleBindingId}` | `userRoleBindingUpdate` (M) | Update User Role Binding. [B] | body `UserRoleBindingInput`; returns `UserRoleBinding` [G] |
| DELETE | `/user-role-bindings/{userRoleBindingId}` | `userRoleBindingDelete` (M) | Delete User Role Binding. [B] | returns `UserRoleBinding` [G] |

### 2.6 Managed Users

Users invited and owned by Spacelift, as opposed to users arriving through
an identity provider, used when `AuthorizationScheme` is `MANAGED_USERS`
**[B][G]**. Groups (`managedUserGroup*`) and their Slack integrations sit here
too.

`AuthorizationScheme` and `UserStatus` **[G]**:

| Value | Description [G] |
|---|---|
| `LOGIN_POLICIES` |   |
| `MANAGED_USERS` |   |

| Value | Description [G] |
|---|---|
| `QUEUED` |   |
| `PENDING` |   |
| `ACTIVE` |   |
| `INACTIVE` |   |

### Managed Users (8)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/managed-user-invite-requests` | `managedUserRequestInvite` (M) | Request Invite. [B] | body `ManagedUserRequestInviteRequest`; returns `Boolean` [G] |
| GET | `/managed-users` | `managedUsers` (Q) | List of users that were invited or logged into the account. [G] | returns `[ManagedUser]` [G] |
| POST | `/managed-users` | `managedUserInviteV2` (M) | Invite Managed User. [B] | body `ManagedUserInviteInputV2`; returns `ManagedUser` [G]; Only for `MANAGED_USERS` accounts (`AuthorizationScheme`) [I] from the enum |
| GET | `/managed-users/{managedUserId}` | `managedUser` (Q) | Details about a single managed user. [G] | returns `ManagedUser` [G] |
| PUT | `/managed-users/{managedUserId}` | `managedUserUpdate` (M) | Update Managed User. [B] | body `ManagedUserUpdateInputBody`; returns `ManagedUser` [G] |
| DELETE | `/managed-users/{managedUserId}` | `managedUserDelete` (M) | Delete Managed User. [B] | returns `ManagedUser` [G] |
| POST | `/managed-users/{managedUserId}/resend-invite` | `managedUserResendInvite` (M) | Resend Invite. [B] | returns `ManagedUser` [G] |
| POST | `/spaces/{spaceId}/role-change-requests` | `managedUserRequestRoleChange` (M) | Request Role Change. [B] | body `ManagedUserRequestRoleChangeRequest`; returns `Boolean` [G] |

### Managed Users: User Groups (11)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/available-user-groups` | `availableUserGroups` (Q) | Returns all user groups that are reported by the identity provider when users log in to Spacelift. [G] | returns `[String]` [G] |
| GET | `/managed-user-group-integrations` | `managedUserGroupIntegrations` (Q) | List of managed user group integrations. Allows for optional integrationType parameter to get integrations of specific type only. [G] | query `integrationType`; returns `[ManagedUserGroupIntegration]` [G] |
| POST | `/managed-user-group-integrations` | `managedUserGroupIntegrationCreate` (M) | Create User Group Integration. [B] | body `ManagedUserGroupIntegrationCreateInput`; returns `ManagedUserGroupIntegration` [G] |
| GET | `/managed-user-group-integrations/{managedUserGroupIntegrationId}` | `managedUserGroupIntegration` (Q) | Managed user group integration specified by id. [G] | returns `ManagedUserGroupIntegration` [G] |
| PUT | `/managed-user-group-integrations/{managedUserGroupIntegrationId}` | `managedUserGroupIntegrationUpdate` (M) | Update User Group Integration. [B] | body `ManagedUserGroupIntegrationUpdateInputBody`; returns `ManagedUserGroupIntegration` [G] |
| DELETE | `/managed-user-group-integrations/{managedUserGroupIntegrationId}` | `managedUserGroupIntegrationDelete` (M) | Delete User Group Integration. [B] | returns `ManagedUserGroupIntegration` [G] |
| GET | `/managed-user-groups` | `managedUserGroups` (Q) | List of user groups in this account. [G] | returns `[ManagedUserGroup]` [G] |
| POST | `/managed-user-groups` | `managedUserGroupCreateV2` (M) | Create User Group. [B] | body `ManagedUserGroupCreateInputV2`; returns `ManagedUserGroup` [G] |
| GET | `/managed-user-groups/{managedUserGroupId}` | `managedUserGroup` (Q) | Details about a single user group. [G] | returns `ManagedUserGroup` [G] |
| PUT | `/managed-user-groups/{managedUserGroupId}` | `managedUserGroupUpdateV2` (M) | Update User Group. [B] | body `ManagedUserGroupUpdateInputV2Body`; returns `ManagedUserGroup` [G] |
| DELETE | `/managed-user-groups/{managedUserGroupId}` | `managedUserGroupDelete` (M) | Delete User Group. [B] | returns `ManagedUserGroup` [G] |

### Managed Users: Fields of `ManagedUserGroup` (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/managed-user-groups/{managedUserGroupId}/managed-users` | `managedUsers` on `ManagedUserGroup` | Members of this group, returned as ManagedUser resolvers. [G] | returns `[ManagedUser]` [G] |
| GET | `/managed-user-groups/{managedUserGroupId}/user-role-bindings` | `userRoleBindings` on `ManagedUserGroup` | List of user group role bindings for this group. [G] | returns `[UserGroupRoleBinding]` [G] |

### Managed Users: Fields of `ManagedUser` (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/managed-users/{managedUserId}/personal-api-keys` | `personalApiKeys` on `ManagedUser` | Personal API keys owned by this user. Root admins only. [G] | returns `[ApiKey]` [G]; Root space admins only [G] |
| GET | `/managed-users/{managedUserId}/user-role-bindings` | `userRoleBindings` on `ManagedUser` | Roles that are binded to this user. [G] | returns `[UserRoleBinding]` [G] |

### 2.7 SSO

SAML and OIDC single sign-on and the SCIM client
[[D]](https://docs.spacelift.io/integrations/single-sign-on). SAML needs the
Enterprise plan, while OIDC single sign-on needs only a paid account
[[D]](https://docs.spacelift.io/integrations/single-sign-on); SCIM needs Enterprise
[[D]](https://docs.spacelift.io/integrations/single-sign-on/scim). An API key
keeps working when SSO is misconfigured, which is the documented way back in
**[B]**.

| Value | Description [G] |
|---|---|
| `TRANSIENT` | An opaque identifier that does not remain the same between logins. |
| `EMAIL_ADDRESS` | An email address. |
| `PERSISTENT` | An opaque identifier that remains the same between logins. |

### SSO (11)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/oidc` | `oidcCreate` (M) | Set up OIDC integration. [G] | body `OidcCreateRequest`; returns `OIDCSettings` [G]; Tier: OIDC single sign-on needs a paid account [[D]](https://docs.spacelift.io/integrations/single-sign-on); no `BillingTierFeature` name matches it [G]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| PUT | `/oidc` | `oidcUpdate` (M) | Update OIDC settings to change claim mappings. [G] | body `OIDCUpdateInput`; returns `OIDCSettings` [G]; Tier: OIDC single sign-on needs a paid account [[D]](https://docs.spacelift.io/integrations/single-sign-on); no `BillingTierFeature` name matches it [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| DELETE | `/oidc` | `oidcDelete` (M) | Delete OIDC integration. [G] | returns `OIDCSettings` [G]; Tier: OIDC single sign-on needs a paid account [[D]](https://docs.spacelift.io/integrations/single-sign-on); no `BillingTierFeature` name matches it [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| GET | `/oidc/settings` | `oidcSettings` (Q) | OIDC settings, if the account is using OIDC-based SSO. [G] | returns `OIDCSettings` [G]; Tier: OIDC single sign-on needs a paid account [[D]](https://docs.spacelift.io/integrations/single-sign-on); no `BillingTierFeature` name matches it [G]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| GET | `/oidc/subject-template` | `oidcSubjectTemplate` (Q) | Return the OIDC subject template for the account. [G] | returns `String` [G]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| POST | `/saml` | `samlCreate` (M) | Set up SAML integration. [G] | body `SamlCreateRequest`; returns `SAMLSettings` [G]; Tier `CUSTOM_SSO_SAML` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/single-sign-on); Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| PUT | `/saml` | `samlUpdate` (M) | Update SAML settings to change claim mappings. [G] | body `SAMLUpdateInput`; returns `SAMLSettings` [G]; Tier `CUSTOM_SSO_SAML` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/single-sign-on); Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| DELETE | `/saml` | `samlDelete` (M) | Delete SAML integration. [G] | returns `SAMLSettings` [G]; Tier `CUSTOM_SSO_SAML` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/single-sign-on); Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| GET | `/saml/settings` | `samlSettings` (Q) | SAML settings, if the account is using SAML-based SSO. [G] | returns `SAMLSettings` [G]; Tier `CUSTOM_SSO_SAML` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/single-sign-on); Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| GET | `/scim-settings` | `scimSettings` (Q) | SCIM settings, if the account has SCIM provisioning configured. [G] | returns `ScimOauthClient` [G]; Tier `SCIM` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/single-sign-on/scim); Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| POST | `/scim-settings` | `createOauthClientForSCIM` (M) | Create an OAuth2 client for SCIM provisioning. [G] | returns `ScimOauthClient` [G]; Tier `SCIM` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/single-sign-on/scim); Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |

### 2.8 Sessions and Security Keys

The current user's login sessions and WebAuthn keys, with revocation, and the
account's security email **[B]**. Session listing and revocation are not on
the Starter tier **[G]**.

| Value | Description [G] |
|---|---|
| `UNKNOWN` |   |
| `WEB` |   |
| `TERRAFORM_CLI` |   |
| `SPACECTL` |   |

### Sessions and Security Keys (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/security-email` | `securityEmail` (Q) | Returns security email for the account. [G] | returns `String` [G]; Bruno marks it advanced: manages your own sessions and security keys [B] |
| GET | `/security-keys` | `securityKeys` (Q) | Returns list of the security keys in the account. [G] | returns `[SecurityKey]` [G]; Bruno marks it advanced: manages your own sessions and security keys [B] |
| DELETE | `/security-keys/{securityKeyId}` | `securityKeyDelete` (M) | Delete a security key. [G] | returns `SecurityKey` [G]; Bruno marks it advanced: manages your own sessions and security keys [B] |
| GET | `/sessions` | `sessions` (Q) | List of all the active sessions in the account. NOTE: this functionality is not available in the Starter tier. An empty array will be returned for Starter tier accounts. [G] | returns `[Session]` [G]; Bruno marks it advanced: manages your own sessions and security keys [B]; Empty list on Starter [G] |
| DELETE | `/sessions/{sessionId}` | `sessionDelete` (M) | Invalidate a Session. NOTE: this functionality is not available in the Starter tier. [G] | returns `Session` [G]; Not on the Starter tier [G]; Bruno marks it advanced: manages your own sessions and security keys [B] |

### 2.9 OAuth

The Slack OAuth redirect **[B]**.

### OAuth (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/slack-integration/oauth/redirect` | `slackOauthRedirect` (M) | Get a Slack URL to redirect to. Optionally specify a Slack environment (defaults to COMMERCIAL). [G] | body `SlackOauthRedirectRequest`; returns `OAuthRedirect` [G]; Bruno marks it advanced: part of a browser-based oauth redirect flow; cannot be completed with an api key [B] |

## 3. Account

### 3.1 Account

Read-only facts about the account: ID, name, type, the AWS account ID
Spacelift assumes roles from, default runner images and outgoing IP
addresses **[G]**.

### Account (7)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/default-private-worker-pool-runner-image` | `defaultPrivateWorkerPoolRunnerImage` (Q) | Return default private worker pool runner image for the account. [G] | returns `String` [G] |
| GET | `/default-public-worker-pool-runner-image` | `defaultPublicWorkerPoolRunnerImage` (Q) | Return default public worker pool runner image for the account. [G] | returns `String` [G] |
| GET | `/id` | `id` (Q) | ID of the account. [G] | returns `ID` [G]; Bruno marks it advanced: account-level scalar exposed as a root query field [B] |
| GET | `/name` | `name` (Q) | Name of the account. [G] | returns `String` [G]; Bruno marks it advanced: account-level scalar exposed as a root query field [B] |
| GET | `/outgoing-ip-addresses` | `outgoingIPAddresses` (Q) | List of Spacelift's outgoing addresses. [G] | returns `[String]` [G] |
| GET | `/spacelift-aws-account-id` | `spaceliftAwsAccountId` (Q) | Returns the AWS account used by Spacelift during role assumption. This can be used for generating AssumeRole policies. [G] | returns `String` [G] |
| GET | `/type` | `type` (Q) | Type of the GitHub account. [G] | returns `AccountType` [G]; Bruno marks it advanced: account-level scalar exposed as a root query field [B] |

### 3.2 Account Settings

Account-wide settings, mostly a query paired with an `account…` mutation:
MFA enforcement, log and audit retention, the OIDC subject template, the
OAuth redirect allowlist, the VCS-triggered run limit (at most 500
[[D]](https://docs.spacelift.io/concepts/run)), AI features, and who may
manage API keys and spaces **[G][B]**.

### Account Settings (42)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/account/can-be-deleted` | `accountCanBeDeleted` (Q) | Whether the grace period of deletion has ended and account can be deleted. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/account/can-be-deleted-at` | `accountCanBeDeletedAt` (Q) | Timestamp when the account can be deleted. [G] | returns `Int` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/confirm-delete` | `accountConfirmDelete` (M) | Delete the account. [G] | returns `Boolean` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-wide administrative setting [B]; Deletes the account [B] |
| POST | `/account/request-infra-assistant-enabling` | `accountRequestInfraAssistantEnabling` (M) | Request an admin to enable Infra Assistant for the account. [G] | returns `Boolean` [G]; Tier `ACCOUNT_AI_CONTROL` [I]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/request-personal-api-keys-enabling` | `accountRequestPersonalApiKeysEnabling` (M) | Request an admin to enable personal API keys for the account. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/set-infra-assistant-build-mode-enabled` | `accountSetInfraAssistantBuildModeEnabled` (M) | Set Infra Assistant build mode enabled state. Requires T&C to be accepted first. [G] | body `AccountSetInfraAssistantBuildModeEnabledRequest`; returns `Boolean` [G]; Tier `ACCOUNT_AI_CONTROL` [I]; Needs the AI terms accepted first [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/set-infra-assistant-enabled` | `accountSetInfraAssistantEnabled` (M) | Set Infra Assistant enabled state. Requires T&C to be accepted first. [G] | body `AccountSetInfraAssistantEnabledRequest`; returns `Boolean` [G]; Tier `ACCOUNT_AI_CONTROL` [I]; Needs the AI terms accepted first [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/set-o-auth-redirect-allowlist` | `accountSetOAuthRedirectAllowlist` (M) | Replace the account's custom OAuth2 redirect host allowlist (exact hostnames or "*.subdomain" wildcards) [G] | body `AccountSetOAuthRedirectAllowlistRequest`; returns `[String]` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/set-oidc-subject-template` | `accountSetOIDCSubjectTemplate` (M) | Set the OIDC subject template for the account. [G] | body `AccountSetOIDCSubjectTemplateRequest`; returns `String` [G]; Tier `OIDC_FEDERATION_TOKEN_GENERATION` [I]; paid accounts [[D]](https://docs.spacelift.io/integrations/cloud-providers/oidc); Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/set-personal-api-keys-enabled` | `accountSetPersonalApiKeysEnabled` (M) | Allow or refuse personal API key creation for the account. With enabled false and disableExistingKeys true, every personal key on the account is disabled. [G] | body `AccountSetPersonalApiKeysEnabledRequest`; returns `Boolean` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/set-spacelift-intelligence-enabled` | `accountSetSpaceliftIntelligenceEnabled` (M) | Set Spacelift Intelligence enabled state. Enabling implies acceptance of the Spacelift Intelligence Terms of Use and enables all sub-features. [G] | body `AccountSetSpaceliftIntelligenceEnabledRequest`; returns `Boolean` [G]; Tier `ACCOUNT_AI_CONTROL` [I]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/toggle-allow-non-root-admin-space-creation` | `accountToggleAllowNonRootAdminSpaceCreation` (M) | Allow/Disallow non-root admins to create subspaces. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/toggle-api-key-management-from-non-humans` | `accountToggleAPIKeyManagementFromNonHumans` (M) | Enable/disable API key creation from non-human sessions. When enabled, non-human sessions (API keys, Admin Stacks) can manage API keys. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/toggle-deletion-mark` | `accountToggleDeletionMark` (M) | Mark/unmark the Account for deletion. [G] | body `AccountToggleDeletionMarkRequest`; returns `Boolean` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/toggle-enforcing-mfa` | `accountToggleEnforcingMFA` (M) | Enable/disable enforcing MFA for the Account. [G] | returns `Boolean` [G]; Tier `MFA` [I]; Enterprise, with SSO on [[D]](https://docs.spacelift.io/product/security/mfa); Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/update-authorization-scheme` | `accountUpdateAuthorizationScheme` (M) | Update the authorization schema used by the account. [G] | body `AccountUpdateAuthorizationSchemeRequest`; returns `AuthorizationScheme` [G]; Bruno marks it advanced: account-wide administrative setting [B]; `LOGIN_POLICIES` or `MANAGED_USERS` (`AuthorizationScheme`) [G] |
| POST | `/account/update-awareness-source-survey` | `accountUpdateAwarenessSourceSurvey` (M) | Update Account's Awareness Source. [G] | body `AccountUpdateAwarenessSourceSurveyRequest`; returns `AwarenessSourceSurvey` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/update-default-worker-pool-runner-images` | `accountUpdateDefaultWorkerPoolRunnerImages` (M) | Update Account's default public worker pool runner image. [G] | body `AccountUpdateDefaultWorkerPoolRunnerImagesRequest`; returns `AccountUpdateDefaultWorkerPoolRunnerImagesResult` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/update-security-email` | `accountUpdateSecurityEmail` (M) | Update Account's Security Email. [G] | body `AccountUpdateSecurityEmailRequest`; returns `String` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/account/update-vcs-event-triggered-runs-limit` | `accountUpdateVCSEventTriggeredRunsLimit` (M) | Update limit of runs created by a single VCS event for this account. [G] | body `AccountUpdateVCSEventTriggeredRunsLimitRequest`; returns `Int` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| POST | `/ai-integrations/{aiIntegrationId}/toggle` | `aiIntegrationToggle` (M) | Enable or disable an AI integration for the calling account. Works for both account-owned and shared Spacelift-provided integrations. Enabling a Spacelift-provided integration requires Spacelift Intelligence to be on. Disabling one is always allowed, so an account can opt out before it turns Spacelift Intelligence on. [G] | body `AiIntegrationToggleRequest`; returns `AIIntegration` [G]; RBAC `AI_INTEGRATION_TOGGLE` [I] by name, fallback Admin [G][D]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/allow-non-root-admin-space-creation` | `allowNonRootAdminSpaceCreation` (Q) | Account-level setting indicating if non-root admins can create subspaces. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/api-keys-manageable-by-non-humans` | `apiKeysManageableByNonHumans` (Q) | Account-level setting indicating non-human sessions (API keys, Admin Stacks) can manage API keys. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/api-keys-managed-by-non-root-admins` | `apiKeysManagedByNonRootAdmins` (Q) | Account-level setting indicating if API keys can be managed by non-root admins. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/audit-trail/retention-days` | `auditTrailRetentionDays` (Q) | How many days built-in audit trails are stored. [G] | returns `Int` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/authorization-scheme` | `authorizationScheme` (Q) | Authorization scheme for the account. [G] | returns `AuthorizationScheme` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/available-ai-models` | `availableAIModels` (Q) | Returns the live list of models available for the given (possibly unsaved) AI integration configuration, used to populate the model picker before the integration is saved. API-key providers (OpenAI/Anthropic/Google): pass provider + apiKey and, for self-hosted gateways, baseURL. Requires admin on the given space. [G] | query `provider`, `apiKey`, `providerIntegrationId`, `providerConfig`, `baseURL`, `space`; returns `[AIModel]` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/available-ai-providers` | `availableAIProviders` (Q) | Returns available AI providers. If provider is specified, returns models for that provider only. If provider is not specified, returns all providers with their available models. [G] | query `provider`; returns `[AIProvider]` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/awareness-source-survey` | `awarenessSourceSurvey` (Q) | Awareness Source Survey. [B] | returns `AwarenessSourceSurvey` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/enforce-mfa` | `enforceMFA` (Q) | EnforceMFA determines whether MFA is required for all users in the account. [G] | returns `Boolean` [G]; Tier `MFA` [I]; Enterprise, with SSO on [[D]](https://docs.spacelift.io/product/security/mfa); Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/has-ansible-stacks` | `hasAnsibleStacks` (Q) | True if the account has at least one Ansible stack. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/has-sso` | `hasSSO` (Q) | Returns true if the account has SSO enabled. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/infra-assistant-build-mode-enabled` | `infraAssistantBuildModeEnabled` (Q) | InfraAssistantBuildModeEnabled determines whether Infra Assistant build mode is enabled. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/infra-assistant-enabled` | `infraAssistantEnabled` (Q) | InfraAssistantEnabled determines whether Infra Assistant is enabled. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/marked-for-deletion` | `markedForDeletion` (Q) | Tells if the account is marked for deletion. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/oauth-redirect-allowed-hosts` | `oauthRedirectAllowedHosts` (Q) | Returns the account's custom OAuth2 redirect host allowlist. [G] | returns `[String]` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/personal-api-keys-enabled` | `personalApiKeysEnabled` (Q) | Account-level setting indicating whether users may create personal API keys. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/run-log-retention-days` | `runLogRetentionDays` (Q) | Amount of days that run logs will be retained for. [G] | returns `Int` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/spacelift-intelligence-enabled` | `spaceliftIntelligenceEnabled` (Q) | SpaceliftIntelligenceEnabled determines whether Spacelift Intelligence is enabled for the account (true if any sub-feature is on). [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/spacelift-intelligence-terms-accepted` | `spaceliftIntelligenceTermsAccepted` (Q) | SpaceliftIntelligenceTermsAccepted informs if the account has accepted the Spacelift Intelligence terms and conditions. [G] | returns `Boolean` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/spacelift-intelligence-terms-latest-version` | `spaceliftIntelligenceTermsLatestVersion` (Q) | SpaceliftIntelligenceTermsLatestVersion returns the latest version of the Spacelift Intelligence terms and conditions. [G] | returns `TermsAndConditionsVersion` [G]; Bruno marks it advanced: account-wide administrative setting [B] |
| GET | `/vcs-event-triggered-runs-limit` | `vcsEventTriggeredRunsLimit` (Q) | Maximum number of runs triggered by a VCS event for this account. [G] | returns `Int` [G]; Bruno marks it advanced: account-wide administrative setting [B] |

### 3.3 Billing

Subscription, tier, seats, trial and usage **[B]**. `tier` returns a
`BillingTier` and `tierFeatures` the `BillingTierFeature` list that report
03, section 10, maps to operations **[G]**. Usage for run minutes and private
workers is in report 03 (`runMinutesUsage`, `workerUsage`).

| Value | Description [G] |
|---|---|
| `FREE` | Free billing tier. |
| `CLOUD` | Cloud billing tier. |
| `ENTERPRISE` | Enterprise billing tier. |
| `V3_FREE` | Free billing tier. |
| `V3_CLOUD` | Cloud billing tier. |
| `V4_FREE` | Free Billing tier. |
| `V4_STARTER` | Starter Billing tier. |
| `V4_STARTER_PLUS` | Starter Plus Billing tier. |
| `V4_BUSINESS` | BUSINESS Billing tier. |
| `V4_ENTERPRISE` | BUSINESS Billing tier. |
| `V5_FREE` | Free Billing tier (V5). |
| `V5_STARTER` | Starter Billing tier (V5). |
| `V5_STARTER_PLUS` | Starter Plus Billing tier (V5). |
| `V5_STARTER_PLUS_2` | Starter Plus 2 Billing tier (V5). |
| `V5_BUSINESS` | Business Billing tier (V5). |
| `V5_ENTERPRISE` | Enterprise Billing tier (V5). |
| `V5_ENTERPRISE_PLUS` | Enterprise Plus Billing tier (V5). |
| `V5_STRATEGIC` | Strategic Billing tier (V5). |

### Billing (14)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/available-billing-addons` | `availableBillingAddons` (Q) | availableBillingAddons returns the list of available billing addons. [G] | returns `[BillingAddon]` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| GET | `/available-self-service-plans` | `availableSelfServicePlans` (Q) | availableSelfServicePlans returns the list of billing plans that can be bought via self-service. [G] | returns `[BillingPlan]` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| GET | `/billed-externally` | `billedExternally` (Q) | Whether the account is billed externally (not through Stripe) [G] | returns `Boolean` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| GET | `/billing-subscription` | `billingSubscription` (Q) | Shows Stripe Billing subscription details. [G] | returns `BillingSubscription` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| POST | `/billing-subscription` | `billingSubscriptionCreate` (M) | Creates a new billing subscription addons argument can be ignored because it has not implemented yet. [G] | body `BillingSubscriptionCreateRequest`; returns `BillingSubscription` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| PUT | `/billing-subscription` | `billingSubscriptionUpdateV2` (M) | Updates an existing billing subscription. [G] | body `SubscriptionUpdateV2Input`; returns `BillingSubscription` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| DELETE | `/billing-subscription` | `billingSubscriptionDelete` (M) | Cancels current subscription. [G] | returns `BillingSubscription` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| POST | `/billing-subscription/update-info` | `billingSubscriptionUpdateInfo` (M) | Updates the billing info of an existing subscription. [G] | body `SubscriptionInput`; returns `BillingSubscription` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| POST | `/billing-subscription/update-tier` | `billingSubscriptionUpdateTier` (M) | Updates the billing tier of an existing billing subscription. [G] | body `BillingSubscriptionUpdateTierRequest`; returns `BillingSubscription` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| GET | `/on-trial-until` | `onTrialUntil` (Q) | If the account is on the trial period, this indicates the end of it. [G] | returns `Int` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| GET | `/seats` | `seats` (Q) | Returns the seat limits and the number of in-use seats. [G] | returns `Seats` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| GET | `/tier` | `tier` (Q) | Billing tier of the account. [G] | returns `BillingTier` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| GET | `/tier-features` | `tierFeatures` (Q) | The list of features available based on the billing tier of the account. [G] | returns `[BillingTierFeature]` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |
| GET | `/usage` | `usage` (Q) | Usage for the last 30 days. [G] | returns `Usage` [G]; Bruno marks it advanced: subscription and billing management, normally handled through spacelift sales [B] |

### 3.4 Feature Flags, Debug and Infrastructure

Installation internals for self-hosted operators: flag evaluation (empty on
SaaS), build and licence details, installation ID, message queue depth and
the OPA version (`policyRuntime`) **[G]**. Report 24 covers self-hosted.

### Feature Flags (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/evaluate-feature-flags` | `evaluateFeatureFlags` (Q) | evaluateFeatureFlags returns the current value of the specified feature flags. This API is only available on Self-Hosted installations, and trying to use it on Spacelift SaaS will result in an empty array being returned. [G] | query `input`; returns `[FeatureFlagEvaluationResult]` [G]; Self-hosted only [G]; Bruno marks it advanced: internal feature rollout state [B]; Empty on SaaS [G] |

### Debug (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/debug-info` | `debugInfo` (Q) | Info about git build related data, self-hosted licence. Not valid in SaaS. [G] | returns `DebugInfo` [G]; Self-hosted only [G]; Bruno marks it advanced: internal diagnostic field [B] |

### Infrastructure (3)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/installation-id` | `installationId` (Q) | ID of the GitHub App installation linked to the current account. [G] | returns `String` [G]; Bruno marks it advanced: internal infrastructure detail [B] |
| GET | `/message-queue-stats` | `messageQueueStats` (Q) | MessageQueueStats returns the current size of every message queue the backend is configured to use. Only available on Self-Hosted instances and can only be fetched by admins. [G] | returns `[MessageQueueStats]` [G]; Admins only [G]; Self-hosted only [G]; Bruno marks it advanced: internal infrastructure detail [B] |
| GET | `/policy-runtime` | `policyRuntime` (Q) | List Policy Runtime Details. [G] | returns `PolicyRuntimeDetails` [G]; Bruno marks it advanced: internal infrastructure detail [B] |

## 4. Source control

### 4.1 VCS Integrations

One integration type per provider: GitHub (the App, plus GitHub
Enterprise), GitLab, Bitbucket Cloud, Bitbucket Data Center, Azure DevOps,
and raw Git for public repositories
[[D]](https://docs.spacelift.io/integrations/source-control). The top level
lists and tests integrations of any provider and browses repositories and
branches **[B]**. Aggregated VCS checks need Business and above
[[D]](https://docs.spacelift.io/integrations/source-control/gitlab); space-level
GitHub integrations need Enterprise
[[D]](https://docs.spacelift.io/integrations/source-control/github). Report 11
records the provider settings.

`VCSProvider`, `VCSCheck` and `VCSIntegrationTestType` **[G]**:

| Value | Description [G] |
|---|---|
| `BITBUCKET_CLOUD` | Bitbucket Cloud (bitbucket.org). |
| `BITBUCKET_DATACENTER` | Self-hosted Bitbucket Datacenter. |
| `GITHUB` | Managed GitHub (github.com). |
| `GITLAB` | GitLab - either self-hosted or managed (gitlab.com). |
| `GITHUB_ENTERPRISE` | GitHub Enterprise - either self-hosted or as an extra organization on github.com. |
| `SHOWCASE` | Spacelift-managed virtual VCS provider used for onboarding new users. |
| `AZURE_DEVOPS` | Azure DevOps. |
| `GIT` | Git link VCS provider. |
| `SPACELIFT` | Spacelift-native VCS provider with files stored in database. |
| `BLOB` | Blob Storage VCS provider reading commit archives from an object store. |
| `ORIGIN` | Origin - Cursor's git platform, integrated via a global Spacelift-owned app. |

| Value | Description [G] |
|---|---|
| `INDIVIDUAL` | a VCS check for each stack. |
| `AGGREGATED` | a single VCS check aggregating all the stacks checks. |
| `ALL` | INDIVIDUAL and AGGREGATED checks. |

| Value | Description [G] |
|---|---|
| `CONNECTION` |   |
| `PERMISSIONS` |   |
| `WEBHOOK` |   |
| `GITHUB_APP_EVENTS` |   |

### VCS Integrations (10)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| PUT | `/azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}` | `azureDevOpsRepoIntegrationUpdate` (M) | Updates the Azure DevOps Repo integration with new properties. [G] | body `AzureDevOpsRepoIntegrationUpdateRequest`; returns `AzureDevOpsRepoIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab); Bruno files it under Danger Zone [B] |
| PUT | `/bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}` | `bitbucketDatacenterIntegrationUpdate` (M) | Updates the Bitbucket Datacenter integration with new properties. [G] | body `BitbucketDatacenterIntegrationUpdateRequest`; returns `BitbucketDatacenterIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab); Bruno files it under Danger Zone [B] |
| GET | `/branches` | `branches` (Q) | List of branches for a given repository. [G] | query `provider`, `namespace`, `repository`, `repositoryURL`, `vcsIntegrationID`; returns `[String]` [G] |
| PUT | `/github-enterprise-integrations/{githubEnterpriseIntegrationId}` | `githubEnterpriseIntegrationUpdate` (M) | Updates the GitHub Enterprise integration. [G] | body `GithubEnterpriseIntegrationUpdateRequest`; returns `GithubEnterpriseIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab); Bruno files it under Danger Zone [B] |
| PUT | `/gitlab-integrations/{gitlabIntegrationId}` | `gitlabIntegrationUpdate` (M) | Updates the GitLab integration with a new access token and (optionally) API host or User Facing Host. [G] | body `GitlabIntegrationUpdateRequest`; returns `GitlabIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab); Bruno files it under Danger Zone [B] |
| GET | `/repositories` | `repositories` (Q) | List of repositories available to the account. Optional filter param can be applied for the results returned from VCS. [G] | query `provider`, `vcsIntegrationID`, `filter`; returns `[Repository]` [G] |
| POST | `/v-cs-integrations/search` | `searchVCSIntegrations` (Q) | Search VCS Integrations. [B] | body `SearchInput`; returns `SearchVCSIntegrationsOutput` [G] |
| GET | `/vcs-integrations` | `vcsIntegrations` (Q) | List of all VCS integrations available in the account. [G] | returns `[VCSIntegration]` [G] |
| GET | `/vcs-integrations/{vcsIntegrationId}` | `vcsIntegration` (Q) | Get VCS integrations by provider and id. [G] | query `provider`; returns `VCSIntegration` [G] |
| GET | `/vcs-integrations/{vcsIntegrationId}/test` | `vcsIntegrationTest` (Q) | Runs a test to check whether the VCS integration is configured correctly. [G] | query `provider`; returns `VCSIntegrationTestResult` [G] |

### VCS Integrations: Azure DevOps (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/azure-dev-ops-repo-integrations` | `azureDevOpsRepoIntegrationCreate` (M) | Creates the Azure DevOps Repo integration. [G] | body `AzureDevOpsRepoIntegrationCreateRequest`; returns `AzureDevOpsRepoIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab) |
| GET | `/azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}` | `azureDevOpsRepoIntegration` (Q) | Shows Azure DevOps Repos integration details. [G] | returns `AzureDevOpsRepoIntegration` [G] |
| DELETE | `/azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}` | `azureDevOpsRepoIntegrationDelete` (M) | Deletes the Azure DevOps Repo integration. [G] | returns `AzureDevOpsRepoIntegration` [G] |
| POST | `/azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}/test` | `azureDevOpsRepoIntegrationTest` (M) | Tests the Azure DevOps Repo integration. [G] | returns `Boolean` [G] |
| GET | `/azure-dev-ops-webhooks-endpoint` | `azureDevOpsWebhooksEndpoint` (Q) | Endpoint for incoming Azure DevOps webhooks. [G] | returns `String` [G] |

### VCS Integrations: Bitbucket Cloud (7)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/bitbucket-cloud-integrations` | `bitbucketCloudIntegrationCreateV2` (M) | Creates the Bitbucket Cloud integration. [G] | body `BitbucketCloudIntegrationInput`; returns `BitbucketCloudIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab) |
| GET | `/bitbucket-cloud-integrations/{bitbucketCloudIntegrationId}` | `bitbucketCloudIntegration` (Q) | Shows Bitbucket Cloud integration details. [G] | returns `BitbucketCloudIntegration` [G] |
| PUT | `/bitbucket-cloud-integrations/{bitbucketCloudIntegrationId}` | `bitbucketCloudIntegrationUpdateV2` (M) | Updates the Bitbucket Cloud integration with new properties. [G] | body `BitbucketCloudIntegrationUpdateInputBody`; returns `BitbucketCloudIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab) |
| DELETE | `/bitbucket-cloud-integrations/{bitbucketCloudIntegrationId}` | `bitbucketCloudIntegrationDelete` (M) | Deletes the Bitbucket Cloud integration. [G] | returns `BitbucketCloudIntegration` [G] |
| POST | `/bitbucket-cloud-integrations/{bitbucketCloudIntegrationId}/regenerate-webhook-secret` | `bitbucketCloudIntegrationRegenerateWebhookSecret` (M) | Regenerates the webhook secret for Bitbucket Cloud integration. [G] | returns `BitbucketCloudIntegration` [G] |
| POST | `/bitbucket-cloud-integrations/{bitbucketCloudIntegrationId}/test` | `bitbucketCloudIntegrationTest` (M) | Tests the Bitbucket Cloud integration. [G] | returns `Boolean` [G] |
| GET | `/bitbucket-cloud-webhooks-endpoint` | `bitbucketCloudWebhooksEndpoint` (Q) | Endpoint for incoming Bitbucket Cloud webhooks. [G] | returns `String` [G] |

### VCS Integrations: Bitbucket Datacenter (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/bitbucket-datacenter-integrations` | `bitbucketDatacenterIntegrationCreate` (M) | Creates the Bitbucket Datacenter integration. [G] | body `BitbucketDatacenterIntegrationCreateRequest`; returns `BitbucketDatacenterIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab) |
| GET | `/bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}` | `bitbucketDatacenterIntegration` (Q) | Shows Bitbucket Datacenter integration details. [G] | returns `BitbucketDatacenterIntegration` [G] |
| DELETE | `/bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}` | `bitbucketDatacenterIntegrationDelete` (M) | Deletes the Bitbucket Datacenter integration. [G] | returns `BitbucketDatacenterIntegration` [G] |
| POST | `/bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}/regenerate-webhook-secret` | `bitbucketDatacenterIntegrationRegenerateWebhookSecret` (M) | Regenerates the webhook secret for Bitbucket Datacenter integration. [G] | returns `BitbucketDatacenterIntegration` [G] |
| POST | `/bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}/test` | `bitbucketDatacenterIntegrationTest` (M) | Tests the Bitbucket Datacenter integration. [G] | returns `Boolean` [G] |
| GET | `/bitbucket-datacenter-webhooks-endpoint` | `bitbucketDatacenterWebhooksEndpoint` (Q) | Endpoint for incoming Bitbucket Datacenter webhooks. [G] | returns `String` [G] |

### VCS Integrations: GitHub (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/github-integration` | `githubIntegration` (Q) | Build-in GitHub Integration settings. [G] | returns `GithubIntegration` [G] |
| PUT | `/github-integration` | `githubIntegrationUpdate` (M) | Updates the build-in GitHub integration. [G] | body `GithubIntegrationUpdateRequest`; returns `GithubIntegration` [G] |

### VCS Integrations: GitHub Enterprise (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/github-enterprise-integrations` | `githubEnterpriseIntegrationCreate` (M) | Creates the GitHub Enterprise integration. [G] | body `GithubEnterpriseIntegrationCreateRequest`; returns `GithubEnterpriseIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab) |
| GET | `/github-enterprise-integrations/{githubEnterpriseIntegrationId}` | `githubEnterpriseIntegration` (Q) | GitHub Enterprise Integration settings. If the id is not provided, it will return the default GitHub Enterprise Integration. [G] | returns `GithubEnterpriseIntegration` [G] |
| DELETE | `/github-enterprise-integrations/{githubEnterpriseIntegrationId}` | `githubEnterpriseIntegrationDelete` (M) | Deletes the GitHub Enterprise integration. [G] | returns `GithubEnterpriseIntegration` [G] |
| POST | `/github-enterprise-integrations/{githubEnterpriseIntegrationId}/regenerate-webhook-secret` | `githubEnterpriseIntegrationRegenerateWebhookSecret` (M) | Regenerates the webhook secret for GitHub Enterprise integration. [G] | returns `GithubEnterpriseIntegration` [G] |
| POST | `/github-enterprise-integrations/{githubEnterpriseIntegrationId}/test` | `githubEnterpriseIntegrationTest` (M) | Tests the GitHub Enterprise integration. [G] | returns `Boolean` [G] |
| GET | `/github-enterprise-webhooks-endpoint` | `githubEnterpriseWebhooksEndpoint` (Q) | The webhooks endpoint to use in your GitHub Enterprise App. [G] | query `id`; returns `String` [G] |

### VCS Integrations: GitLab (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/gitlab-integrations` | `gitlabIntegrationCreate` (M) | Creates the GitLab integration. [G] | body `GitlabIntegrationCreateRequest`; returns `GitlabIntegration` [G]; Tier `AGGREGATED_VCS_CHECKS` [I]; aggregated checks: Business and above [[D]](https://docs.spacelift.io/integrations/source-control/gitlab) |
| GET | `/gitlab-integrations/{gitlabIntegrationId}` | `gitlabIntegration` (Q) | Shows Gitlab integration details. If the id is not provided, it will return the default Gitlab Integration. [G] | returns `GitlabIntegration` [G] |
| DELETE | `/gitlab-integrations/{gitlabIntegrationId}` | `gitlabIntegrationDelete` (M) | Deletes the GitLab integration. [G] | returns `GitlabIntegration` [G] |
| POST | `/gitlab-integrations/{gitlabIntegrationId}/regenerate-webhook-secret` | `gitlabIntegrationRegenerateWebhookSecret` (M) | Regenerates the webhook secret for GitLab integration. [G] | returns `GitlabIntegration` [G] |
| POST | `/gitlab-integrations/{gitlabIntegrationId}/test` | `gitlabIntegrationTest` (M) | Tests the GitLab integration. [G] | returns `Boolean` [G] |
| GET | `/gitlab-webhooks-endpoint` | `gitlabWebhooksEndpoint` (Q) | Endpoint for incoming Gitlab webhooks. [G] | returns `String` [G] |

### VCS Integrations: Fields of `VCSIntegration` (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/vcs-integrations/{vcsIntegrationId}/search-users` | `searchUsers` on `VCSIntegration` | Search stacks/modules using this VCS integration. [G] | query `input`; returns `SearchVCSIntegrationUsersOutput` [G] |
| GET | `/vcs-integrations/{vcsIntegrationId}/search-users-suggestions` | `searchUsersSuggestions` on `VCSIntegration` | Search stacks/modules suggestions using this VCS integration. [G] | query `input`; returns `SearchSuggestionsOutput` [G] |

### 4.2 VCS Agent Pools

Pools of agents inside a private network that relay VCS traffic Spacelift
cannot reach directly; Enterprise only
[[D]](https://docs.spacelift.io/concepts/vcs-agent-pools).

### VCS Agent Pools (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/vcs-agent-pools` | `vcsAgentPools` (Q) | List of all the VCSAgentPools in the account. [G] | returns `[VCSAgentPool]` [G]; Tier `PRIVATE_VCS` [I]; Enterprise [[D]](https://docs.spacelift.io/concepts/vcs-agent-pools) |
| POST | `/vcs-agent-pools` | `vcsAgentPoolCreate` (M) | Create a new VCSAgentPool. [G] | body `VcsAgentPoolCreateRequest`; returns `VCSAgentPool` [G]; Tier `PRIVATE_VCS` [I]; Enterprise [[D]](https://docs.spacelift.io/concepts/vcs-agent-pools) |
| GET | `/vcs-agent-pools/{vcsAgentPoolId}` | `vcsAgentPool` (Q) | Details of a single VCSAgentPool. [G] | returns `VCSAgentPool` [G]; Tier `PRIVATE_VCS` [I]; Enterprise [[D]](https://docs.spacelift.io/concepts/vcs-agent-pools) |
| PUT | `/vcs-agent-pools/{vcsAgentPoolId}` | `vcsAgentPoolUpdate` (M) | Update an existing VCSAgentPool. [G] | body `VcsAgentPoolUpdateRequest`; returns `VCSAgentPool` [G]; Tier `PRIVATE_VCS` [I]; Enterprise [[D]](https://docs.spacelift.io/concepts/vcs-agent-pools) |
| DELETE | `/vcs-agent-pools/{vcsAgentPoolId}` | `vcsAgentPoolDelete` (M) | Delete an existing VCSAgentPool. [G] | returns `VCSAgentPool` [G]; Tier `PRIVATE_VCS` [I]; Enterprise [[D]](https://docs.spacelift.io/concepts/vcs-agent-pools) |
| POST | `/vcs-agent-pools/{vcsAgentPoolId}/reset` | `vcsAgentPoolReset` (M) | Recreate the credentials necessary to connect VCSAgentPool's agents to the gateways. [G] | returns `VCSAgentPool` [G]; Tier `PRIVATE_VCS` [I]; Enterprise [[D]](https://docs.spacelift.io/concepts/vcs-agent-pools); New credentials for the pool's agents [I] by analogy with `workerPoolReset` |

### 4.3 Repos

Spacelift's built-in repositories (Repos) with commits and file search
[[D]](https://docs.spacelift.io/concepts/repos). The RBAC catalog has repo
create, update and delete and a commit action **[G][D]**.

### Repos (11)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/repo-commit` | `repoCommit` (Q) | Get a Repo commit by repo ID and SHA (returns latest if SHA not provided) [G] | query `repoID`, `sha`; returns `RepoCommit` [G] |
| POST | `/repo-commits` | `repoCommitCreate` (M) | Create a commit in a Repo. [G] | body `RepoCommitCreateInput`; returns `RepoCommit` [G]; RBAC `REVISION_CREATE` [I] by name, fallback Writer [G][D] |
| GET | `/repos` | `repos` (Q) | List all Repos in a space. [G] | query `spaceID`, `first`, `after`; returns `ReposOutput` [G] |
| POST | `/repos` | `repoCreate` (M) | Create a new Repo. [G] | body `RepoCreateInput`; returns `Repo` [G]; RBAC `REPO_CREATE` [I] by name, fallback Admin [G][D] |
| POST | `/repos/search` | `searchRepos` (Q) | Search Repos with filtering and ordering. [G] | body `SearchInput`; returns `SearchReposOutput` [G] |
| GET | `/repos/{repoId}` | `repo` (Q) | Get a single Repo by ID. [G] | returns `Repo` [G] |
| PUT | `/repos/{repoId}` | `repoUpdate` (M) | Update an existing Repo. [G] | body `RepoUpdateInput`; returns `Repo` [G]; RBAC `REPO_UPDATE` [I] by name, fallback Writer [G][D] |
| DELETE | `/repos/{repoId}` | `repoDelete` (M) | Delete a Repo (only if no stacks attached) [G] | returns `Boolean` [G]; RBAC `REPO_DELETE` [I] by name, fallback Admin [G][D] |
| POST | `/repos/{repoId}/commits/search` | `searchRepoCommits` (Q) | Search Repo commits with filtering, ordering, and pagination. [G] | body `SearchInput`; returns `SearchRepoCommitsOutput` [G] |
| GET | `/repos/{repoId}/file-history` | `repoFileHistory` (Q) | Get the commit history of a specific file in a Repo. [G] | query `filePath`, `first`, `after`; returns `RepoFileHistoryOutput` [G] |
| POST | `/repos/{repoId}/files/search` | `searchRepoFiles` (Q) | Search Repo files with filtering, ordering, and pagination. [G] | body `SearchRepoFilesRequest`; returns `SearchRepoFilesOutput` [G] |

### Repos: Fields of `Repo` (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/repos/{repoId}/commits` | `commits` on `Repo` | List of commits in this repo. [G] | query `first`, `after`; returns `RepoCommitsOutput` [G] |

## 5. Cloud and storage integrations

### 5.1 Cloud Integrations

AWS and Azure integrations are created once and attached to stacks, modules
or intent projects; an unattached integration does nothing **[B]**
[[D]](https://docs.spacelift.io/integrations/cloud-providers). Attachments
carry `read` and `write` flags; an integration auto-attached by
`autoattach:<label>` gets both, and `autoattach_read:<label>` or
`autoattach_write:<label>` limit it to one
[[D]](https://docs.spacelift.io/concepts/configuration/context).
`stackIntegrationAwsCreate` and `stackIntegrationAwsDelete` are the older
per-stack AWS role, which the Terraform provider's `spacelift_aws_role` still
calls (report 05) **[G]**. GCP stack integrations are deprecated in the
schema and excluded from the reference **[G]**. Report 12 records the model.

### Cloud Integrations: AWS (14)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| PUT | `/aws-integration-attachments/{attachmentId}` | `awsIntegrationAttachmentUpdate` (M) | Updates an AWS integration attachment. [G] | body `AwsIntegrationAttachmentUpdateRequest`; returns `AwsIntegrationStackAttachment` [G]; RBAC `STACK_AWS_INTEGRATION_UPDATE` [I] by name, fallback Admin (schema: disabled) [G][D] |
| DELETE | `/aws-integration-attachments/{attachmentId}` | `awsIntegrationDetach` (M) | Detaches an AWS integration from a Stack, using the unique ID of the attachment. [G] | returns `AwsIntegrationStackAttachment` [G]; RBAC `STACK_AWS_INTEGRATION_DETACH` [I] by name, fallback Admin (schema: disabled) [G][D] |
| GET | `/aws-integration-by-name` | `awsIntegrationByName` (Q) | Details of a single AWS integration looked up by the integration name. [G] | query `name`; returns `AwsIntegration` [G] |
| GET | `/aws-integrations` | `awsIntegrations` (Q) | Lists any AWS integrations connected to the account. By default this excludes any integrations that were created via the stackIntegrationAwsCreate mutation. You can return all integrations by setting includeLegacy to true. [G] | query `includeLegacy`; returns `[AwsIntegration]` [G] |
| POST | `/aws-integrations` | `awsIntegrationCreate` (M) | Creates an AWS integration. [G] | body `AwsIntegrationCreateRequest`; returns `AwsIntegration` [G] |
| GET | `/aws-integrations-enabled` | `awsIntegrationsEnabled` (Q) | Indicates whether the managed AWS integration functionality is enabled. [G] | returns `Boolean` [G] |
| GET | `/aws-integrations/{awsIntegrationId}` | `awsIntegration` (Q) | Details of a single AWS integration. [G] | returns `AwsIntegration` [G] |
| PUT | `/aws-integrations/{awsIntegrationId}` | `awsIntegrationUpdate` (M) | Updates the integration with the specified ID. [G] | body `AwsIntegrationUpdateRequest`; returns `AwsIntegration` [G] |
| DELETE | `/aws-integrations/{awsIntegrationId}` | `awsIntegrationDelete` (M) | Deletes the integration with the specified ID. [G] | returns `AwsIntegration` [G] |
| PUT | `/intent-projects/{intentProjectId}/aws-integrations/{integrationId}` | `awsIntegrationIntentProjectAttach` (M) | Attach AWS integration to an intent project. [G] | body `AwsIntegrationIntentProjectAttachRequest`; returns `AwsIntegrationIntentProjectAttachment` [G]; RBAC `INTENT_PROJECT_CLOUD_INTEGRATION_ATTACH` [I] by name, fallback Admin [G][D] |
| DELETE | `/intent-projects/{intentProjectId}/aws-integrations/{integrationId}` | `awsIntegrationIntentProjectDetach` (M) | Detach AWS integration from an intent project. [G] | returns `AwsIntegrationIntentProjectAttachment` [G]; RBAC `INTENT_PROJECT_CLOUD_INTEGRATION_DETACH` [I] by name, fallback Admin [G][D] |
| PUT | `/stacks/{stackId}/aws-integrations/{awsIntegrationId}` | `awsIntegrationAttach` (M) | Attaches the specified AWS integration to a Stack. Note that at least one of either the 'read' or 'write' parameters must be true, and a Stack can have at most one read, and one write attachment. A single integration can be attached for both read and write. [G] | body `AwsIntegrationAttachRequest`; returns `AwsIntegrationStackAttachment` [G]; RBAC `STACK_AWS_INTEGRATION_ATTACH` [I] by name, fallback Admin (schema: disabled) [G][D] |
| PUT | `/stacks/{stackId}/aws-role` | `stackIntegrationAwsCreate` (M) | Create a stack AWS integration by setting a role ARN. [G] | body `StackIntegrationAwsCreateRequest`; returns `StackIntegrationAws` [G] |
| DELETE | `/stacks/{stackId}/aws-role` | `stackIntegrationAwsDelete` (M) | Delete an AWS integration by unsetting a role ARN. [G] | returns `StackIntegrationAws` [G] |

### Cloud Integrations: Azure (10)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| PUT | `/azure-integration-attachments/{attachmentId}` | `azureIntegrationAttachmentUpdate` (M) | Updates an Azure integration attachment. [G] | body `AzureIntegrationAttachmentUpdateRequest`; returns `AzureIntegrationStackAttachment` [G]; RBAC `STACK_AZURE_INTEGRATION_UPDATE` [I] by name, fallback Admin (schema: disabled) [G][D] |
| DELETE | `/azure-integration-attachments/{attachmentId}` | `azureIntegrationDetach` (M) | Detaches an Azure integration from a Stack, using the unique ID of the attachment. [G] | returns `AzureIntegrationStackAttachment` [G]; RBAC `STACK_AZURE_INTEGRATION_DETACH` [I] by name, fallback Admin (schema: disabled) [G][D] |
| GET | `/azure-integration-by-name` | `azureIntegrationByName` (Q) | Details of a single Azure integration looked up by the integration name. [G] | query `name`; returns `AzureIntegration` [G] |
| GET | `/azure-integrations` | `azureIntegrations` (Q) | Lists any Azure integrations connected to the account. [G] | returns `[AzureIntegration]` [G] |
| POST | `/azure-integrations` | `azureIntegrationCreate` (M) | Creates an Azure integration. [G] | body `AzureIntegrationCreateRequest`; returns `AzureIntegration` [G] |
| GET | `/azure-integrations-enabled` | `azureIntegrationsEnabled` (Q) | Indicates whether the managed Azure integration functionality is enabled. [G] | returns `Boolean` [G] |
| GET | `/azure-integrations/{azureIntegrationId}` | `azureIntegration` (Q) | Details of a single Azure integration. If the id is not provided, it will return the default Azure Integration. [G] | returns `AzureIntegration` [G] |
| PUT | `/azure-integrations/{azureIntegrationId}` | `azureIntegrationUpdate` (M) | Updates the integration with the specified ID. [G] | body `AzureIntegrationUpdateRequest`; returns `AzureIntegration` [G] |
| DELETE | `/azure-integrations/{azureIntegrationId}` | `azureIntegrationDelete` (M) | Deletes the integration with the specified ID. [G] | returns `AzureIntegration` [G] |
| PUT | `/stacks/{stackId}/azure-integrations/{azureIntegrationId}` | `azureIntegrationAttach` (M) | Attaches the specified Azure integration to a Stack. Note that at least one of either the 'read' or 'write' parameters must be true, and a Stack can have at most one read, and one write attachment. A single integration can be attached for both read and write. [G] | body `AzureIntegrationAttachRequest`; returns `AzureIntegrationStackAttachment` [G]; RBAC `STACK_AZURE_INTEGRATION_ATTACH` [I] by name, fallback Admin (schema: disabled) [G][D] |

### Cloud Integrations: Fields of `AwsIntegration` (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/aws-integrations/{awsIntegrationId}/attached-stack` | `attachedStack` on `AwsIntegration` | Retrieve an individual stack attachment by its ID (slug). [G] | query `id`; returns `AwsIntegrationStackAttachment` [G] |
| GET | `/aws-integrations/{awsIntegrationId}/external-id-for-stack` | `externalIdForStack` on `AwsIntegration` | Generates the external ID that would be used for role assumption when attached to the specified stack. [G] | query `stackId`, `read`, `write`; returns `ExternalIdForStackResult` [G] |

### Cloud Integrations: Fields of `AzureIntegration` (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/azure-integrations/{azureIntegrationId}/attached-stack` | `attachedStack` on `AzureIntegration` | Retrieve an individual stack attachment by its ID (slug). [G] | query `id`; returns `AzureIntegrationStackAttachment` [G] |

### 5.2 Blob Storage

An integration that keeps large run artifacts in the customer's own object
store **[B]**. `BlobStorageSubtype` and `BlobStorageAuthMethod` **[G]**:

| Value | Description [G] |
|---|---|
| `AWS_S3` | AWS S3, read with an assumed role or anonymously. |
| `S3_COMPATIBLE` | An S3-compatible store (e.g. MinIO, Ceph) at a custom endpoint. |

| Value | Description [G] |
|---|---|
| `ANONYMOUS` | Anonymous reads from a public bucket. |
| `AWS_ROLE` | An assumed AWS IAM role (AWS S3 subtype only). |
| `ACCESS_KEYS` | A static access key and secret (S3-compatible subtype only). |

### Blob Storage (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/blob-storage-integrations` | `blobStorageIntegrationCreate` (M) | Creates a Blob Storage integration. [G] | body `BlobStorageIntegrationCreateRequest`; returns `BlobStorageIntegration` [G] |
| GET | `/blob-storage-integrations/{blobStorageIntegrationId}` | `blobStorageIntegration` (Q) | Shows Blob Storage integration details. [G] | returns `BlobStorageIntegration` [G] |
| PUT | `/blob-storage-integrations/{blobStorageIntegrationId}` | `blobStorageIntegrationUpdate` (M) | Updates a Blob Storage integration with new properties. [G] | body `BlobStorageIntegrationUpdateRequest`; returns `BlobStorageIntegration` [G]; Bruno files it under Danger Zone [B] |
| DELETE | `/blob-storage-integrations/{blobStorageIntegrationId}` | `blobStorageIntegrationDelete` (M) | Deletes a Blob Storage integration. Fails while stacks are still attached to it. [G] | returns `BlobStorageIntegration` [G] |

## 6. Events, notifications and audit

### 6.1 Webhooks

Account-wide named webhooks with custom headers and delivery history;
per-stack webhooks are in report 03 **[B]**. Webhooks need a paid account
[[D]](https://docs.spacelift.io/integrations/webhooks). Report 19 records
payloads.

### Webhooks (7)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/named-webhooks-integrations` | `namedWebhooksIntegrationCreate` (M) | Create a new NamedWebhooksIntegration for a space. [G] | body `NamedWebhooksIntegrationInput`; returns `NamedWebhooksIntegration` [G]; Tier `RUN_STATE_CHANGE_WEBHOOKS` [I]; paid accounts [[D]](https://docs.spacelift.io/integrations/webhooks) |
| POST | `/named-webhooks-integrations/search` | `searchNamedWebhooksIntegrations` (Q) | List Webhooks. [B] | body `SearchInput`; returns `SearchNamedWebhooksIntegrationOutput` [G] |
| GET | `/named-webhooks-integrations/{namedWebhooksIntegrationId}` | `namedWebhooksIntegration` (Q) | Get Webhook. [B] | returns `NamedWebhooksIntegration` [G] |
| PUT | `/named-webhooks-integrations/{namedWebhooksIntegrationId}` | `namedWebhooksIntegrationUpdate` (M) | Update a NamedWebhooksIntegration for a space. [G] | body `NamedWebhooksIntegrationInput`; returns `NamedWebhooksIntegration` [G]; Tier `RUN_STATE_CHANGE_WEBHOOKS` [I]; paid accounts [[D]](https://docs.spacelift.io/integrations/webhooks) |
| DELETE | `/named-webhooks-integrations/{namedWebhooksIntegrationId}` | `namedWebhooksIntegrationDelete` (M) | Delete a NamedWebhooksIntegration from a space. [G] | returns `NamedWebhooksIntegration` [G] |
| POST | `/named-webhooks-integrations/{namedWebhooksIntegrationId}/delete-headers` | `namedWebhooksIntegrationDeleteHeaders` (M) | Remove secret headers for a named webhook. [G] | body `NamedWebhooksIntegrationDeleteHeadersRequest`; returns `NamedWebhooksIntegration` [G] |
| POST | `/named-webhooks-integrations/{namedWebhooksIntegrationId}/set-headers` | `namedWebhooksIntegrationSetHeaders` (M) | Set secret headers for a named webhook to use when sending requests. [G] | body `NamedWebhookHeaderInput`; returns `NamedWebhooksIntegration` [G] |

### Webhooks: Fields of `NamedWebhooksIntegration` (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/named-webhooks-integrations/{namedWebhooksIntegrationId}/deliveries` | `deliveries` on `NamedWebhooksIntegration` | Paginated list of deliveries from this integration. The optional opaque token is used for pagination and should be copied from the previous WebhookDeliveries response. [G] | query `token`; returns `WebhookDeliveries` [G] |
| GET | `/named-webhooks-integrations/{namedWebhooksIntegrationId}/delivery-details` | `deliveryDetails` on `NamedWebhooksIntegration` | Extended details of a single delivery. [G] | query `key`; returns `WebhookDeliveryDetails` [G] |

### 6.2 Audit Trail

The audit log search and the single audit webhook, with headers **[B]**;
Enterprise only [[D]](https://docs.spacelift.io/integrations/audit-trail).

| Value | Description [G] |
|---|---|
| `CREATE` | A new resource was created. |
| `UPDATE` | An existing resource was updated. |
| `DELETE` | An existing resource was deleted. |
| `NONE` | No changes were made - this audit entry is informational only. |
| `UNKNOWN` | The event type is unknown. |

### Audit Trail (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/audit-trail-entries/search` | `searchAuditTrailEntries` (Q) | Searches for audit trail entries. [G] | body `SearchInput`; returns `SearchAuditTrailEntriesOutput` [G]; Tier `AUDITING` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/audit-trail) |
| POST | `/audit-trail/delete-webhook` | `auditTrailDeleteWebhook` (M) | Delete audit trail webhook. [G] | returns `AuditTrailWebhook` [G]; Tier `AUDITING` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/audit-trail); Bruno files it under Danger Zone [B] |
| POST | `/audit-trail/set-webhook` | `auditTrailSetWebhook` (M) | Set audit trail webhook for the Account. [G] | body `AuditTrailWebhookInput`; returns `AuditTrailWebhook` [G]; Tier `AUDITING` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/audit-trail); One audit webhook per account [I] from the singleton route |
| GET | `/audit-trail/webhook` | `auditTrailWebhook` (Q) | Show details of AuditTrailWebhook. [G] | returns `AuditTrailWebhook` [G]; Tier `AUDITING` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/audit-trail) |
| POST | `/audit-trail/webhook-delete-headers` | `auditTrailWebhookDeleteHeaders` (M) | Delete Audit Trail Webhook Headers. [B] | body `AuditTrailWebhookDeleteHeadersInput`; returns `AuditTrailWebhook` [G]; Tier `AUDITING` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/audit-trail); Bruno files it under Danger Zone [B] |
| POST | `/audit-trail/webhook-set-headers` | `auditTrailWebhookSetHeaders` (M) | Set Audit Trail Webhook Headers. [B] | body `AuditTrailWebhookSetHeadersInput`; returns `AuditTrailWebhook` [G]; Tier `AUDITING` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/audit-trail); Bruno files it under Danger Zone [B] |

### 6.3 Notifications

The in-app notification inbox, fed by notification policies **[B]**
[[D]](https://docs.spacelift.io/product/notifications).

| Value | Description [G] |
|---|---|
| `INFO` |   |
| `WARNING` |   |
| `ERROR` |   |

| Value | Description [G] |
|---|---|
| `AWS_INTEGRATION` |   |
| `POLICY` |   |
| `STACK` |   |
| `WORKER_POOL` |   |
| `ACCOUNT` |   |
| `MODULE` |   |

### Notifications (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/notification-count` | `notificationCount` (Q) | Total number of new Notifications. [G] | returns `Int` [G]; Tier `NOTIFICATION_POLICIES` [I]; paid accounts [[D]](https://docs.spacelift.io/product/notifications); Bruno marks it advanced: manages the in-app notification inbox [B] |
| POST | `/notification-groups/{groupingKey}/dismiss` | `dismissNotificationGroup` (M) | Dismisses notifications by grouping key. [G] | returns `[Notification]` [G]; Bruno marks it advanced: manages the in-app notification inbox [B] |
| POST | `/notifications/dismiss` | `dismissNotifications` (M) | Dismisses notifications. [G] | body `DismissNotificationsRequest`; returns `[Notification]` [G]; Bruno marks it advanced: manages the in-app notification inbox [B] |
| POST | `/notifications/dismiss-all` | `dismissAllNotifications` (M) | Dismisses all notifications. [G] | returns `Int` [G]; Bruno marks it advanced: manages the in-app notification inbox [B] |
| POST | `/notifications/search` | `searchNotifications` (Q) | Search Notifications with a set of predicates, ordering and paging. [G] | body `SearchInput`; returns `SearchNotificationsOutput` [G]; Tier `NOTIFICATION_POLICIES` [I]; paid accounts [[D]](https://docs.spacelift.io/product/notifications); Bruno marks it advanced: manages the in-app notification inbox [B] |

### 6.4 Slack

The Slack workspace integration and app configuration, plus the GitHub App
manifest flow **[B]** [[D]](https://docs.spacelift.io/integrations/chatops/slack).

### Slack (7)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/github-app-manifests` | `githubAppGenerateManifest` (M) | Create data for the GitHub app creation form - internal. [G] | body `GitHubAppManifestInput`; returns `GitHubAppManifest` [G]; Bruno marks it advanced: account-level slack or github app configuration [B] |
| POST | `/github-enterprise-integrations/from-manifest` | `githubAppCreateFromManifest` (M) | Create a GitHub integration using an app created from a manifest. [G] | body `GithubAppCreateFromManifestRequest`; returns `GithubEnterpriseIntegration` [G]; Bruno marks it advanced: account-level slack or github app configuration [B]; Completes the GitHub App manifest flow started by `githubAppGenerateManifest` [I] from the names |
| GET | `/slack-app-config` | `slackAppConfig` (Q) | Returns the instance-wide Slack App config information for a Self-Hosted instance. This is used by the Spacelift Slack integration to integrate with your Slack workspace. Always returns null on SaaS. [G] | returns `SlackAppConfig` [G]; Bruno marks it advanced: account-level slack or github app configuration [B] |
| DELETE | `/slack-app-config` | `slackAppConfigDelete` (M) | Deletes the instance-wide Slack configuration. Only available on Self-Hosted instances. Calling this mutation on a Spacelift SaaS account will result in an error. [G] | returns `SlackAppConfig` [G]; Self-hosted only [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-level slack or github app configuration [B] |
| POST | `/slack-app-config/set` | `slackAppConfigSet` (M) | Sets the instance-wide Slack App configuration. This is used to connect your Spacelift installation to a Slack App that can be used for the Slack integration. Only available on Self-Hosted instances. Calling this mutation on a Spacelift SaaS account will result in an error. [G] | body `SlackAppConfigInput`; returns `SlackAppConfig` [G]; Self-hosted only [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-level slack or github app configuration [B] |
| GET | `/slack-app-manifest` | `slackAppManifest` (Q) | Returns the app manifest that should be used when creating a Slack App to integrate Spacelift with Slack. This is only useful on Self-Hosted instances. Always returns empty on SaaS. [G] | returns `String` [G]; Bruno marks it advanced: account-level slack or github app configuration [B] |
| GET | `/slack-integration` | `slackIntegration` (Q) | Shows the details of the integration between your Slack workspace and your Spacelift account. [G] | returns `SlackIntegration` [G]; Bruno marks it advanced: account-level slack or github app configuration [B] |

### 6.5 External Integrations

Third-party integrations registered against the account **[B]**, such as
ServiceNow (Enterprise) and Backstage
[[D]](https://docs.spacelift.io/integrations/external-integrations/servicenow)
[[D]](https://docs.spacelift.io/integrations/external-integrations/backstage).

### External Integrations (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/external-integrations` | `externalIntegrations` (Q) | List External Integrations. [B] | returns `[ExternalIntegration]` [G] |
| POST | `/external-integrations` | `externalIntegrationCreate` (M) | Create External Integration. [B] | body `ExternalIntegrationInput`; returns `ExternalIntegration` [G]; Tier `SERVICE_NOW_INTEGRATION` [I]; Enterprise for ServiceNow [[D]](https://docs.spacelift.io/integrations/external-integrations/servicenow) |
| GET | `/external-integrations/{externalIntegrationId}` | `externalIntegration` (Q) | Get External Integration. [B] | returns `ExternalIntegration` [G] |
| DELETE | `/external-integrations/{externalIntegrationId}` | `externalIntegrationDelete` (M) | Delete External Integration. [B] | returns `ExternalIntegration` [G] |

### 6.6 Origin Integration

Settings of the built-in Origin (Cursor) integration; the read is null when
the app is not installed **[B]**. No docs page describes it
*(unverified)*.

### Origin Integration (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/origin-integration` | `originIntegration` (Q) | Built-in Origin (Cursor) integration settings. Null when the Origin app is not installed on the account. [G] | returns `OriginIntegration` [G] |
| PUT | `/origin-integration` | `originIntegrationUpdate` (M) | Updates the built-in Origin (Cursor) integration. [G] | body `OriginIntegrationUpdateRequest`; returns `OriginIntegration` [G] |
| DELETE | `/origin-integration` | `originIntegrationDelete` (M) | Uninstalls the built-in Origin (Cursor) integration for the account. [G] | returns `Boolean` [G]; Bruno files it under Danger Zone [B] |
| POST | `/origin-integration/install-url` | `originIntegrationInstallURL` (M) | Returns the Origin (Cursor) consent URL that an account admin should be sent to in order to install the global Spacelift Origin app on their namespace. [G] | body `OriginIntegrationInstallURLRequest`; returns `String` [G] |

## 7. Registries, versions and OpenTofu

### 7.1 Terraform Providers

The private provider registry: create the provider, create a version,
register each OS and architecture, optionally upload docs, publish; revoke
withdraws a version **[B]**. Business and above
[[D]](https://docs.spacelift.io/vendors/terraform/provider-registry). Report 15
records the protocol.

| Value | Description [G] |
|---|---|
| `DRAFT` | The version currently being built. |
| `PUBLISHED` | The version is published, and ready to be used. |
| `REVOKED` | The version has been revoked and can no longer be used. |

### Terraform Providers (16)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/providers` | `providers` (Q) | List of all the VCS providers available in the account. [G] | returns `[VCSProvider]` [G] |
| GET | `/providers-in-space` | `providersInSpace` (Q) | List of all the VCS providers available in a specific space. [G] | query `id`; returns `[VCSProvider]` [G] |
| POST | `/terraform-provider-versions` | `terraformProviderVersionCreate` (M) | Create a new Terraform provider version. [G] | body `TerraformProviderVersionCreateRequest`; returns `NewTerraformProviderVersion` [G]; RBAC `TERRAFORM_PROVIDER_VERSION_CREATE` [I] by name, fallback Writer [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| PUT | `/terraform-provider-versions/{terraformProviderVersionId}` | `terraformProviderVersionUpdate` (M) | Update an existing Terraform provider version. [G] | body `TerraformProviderVersionUpdateRequest`; returns `TerraformProviderVersion` [G]; RBAC `TERRAFORM_PROVIDER_VERSION_UPDATE` [I] by name, fallback Writer [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| DELETE | `/terraform-provider-versions/{terraformProviderVersionId}` | `terraformProviderVersionDelete` (M) | Delete a draft Terraform provider version. [G] | returns `TerraformProviderVersion` [G]; RBAC `TERRAFORM_PROVIDER_VERSION_DELETE` [I] by name, fallback Writer [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| POST | `/terraform-provider-versions/{terraformProviderVersionId}/publish` | `terraformProviderVersionPublish` (M) | Publish an existing Terraform provider version. [G] | returns `TerraformProviderVersion` [G]; RBAC `TERRAFORM_PROVIDER_VERSION_PUBLISH` [I] by name, fallback Writer [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| POST | `/terraform-provider-versions/{terraformProviderVersionId}/register-platform` | `terraformProviderVersionRegisterPlatformV2` (M) | This mutation is similar to terraformProviderVersionRegisterPlatform, but it also returns the optional uploadHeaders required for the upload request. [G] | body `TerraformProviderVersionPlatformInput`; returns `TerraformProviderVersionRegisterPlatformV2Output` [G]; RBAC `TERRAFORM_PROVIDER_VERSION_REGISTER_PLATFORM` [I] by name, fallback Writer [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| POST | `/terraform-provider-versions/{terraformProviderVersionId}/revoke` | `terraformProviderVersionRevoke` (M) | Revoke a published Terraform provider version. [G] | returns `TerraformProviderVersion` [G]; RBAC `TERRAFORM_PROVIDER_VERSION_REVOKE` [I] by name, fallback Writer [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| POST | `/terraform-provider-versions/{terraformProviderVersionId}/upload-docs` | `terraformProviderVersionUploadDocs` (M) | Upload documentation for a Terraform provider version. [G] | body `TerraformProviderVersionUploadDocsRequest`; returns `TerraformProviderVersion` [G]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| POST | `/terraform-provider-versions/{terraformProviderVersionId}/verify` | `terraformProviderVersionVerify` (M) | Verify the GPG signature of the SHA256SUMS file, confirm the file contains entries for all registered platform archives and verify their checksums. [G] | returns `TerraformProviderVersion` [G]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| GET | `/terraform-providers` | `terraformProviders` (Q) | List of all Terraform providers managed by this account. [G] | returns `[TerraformProvider]` [G] |
| POST | `/terraform-providers` | `terraformProviderCreate` (M) | Creates a new Terraform provider. Note that the type needs to be unique within the account, and it can then be used as the provider's ID. [G] | body `TerraformProviderCreateRequest`; returns `TerraformProvider` [G]; RBAC `TERRAFORM_PROVIDER_CREATE` [I] by name, fallback Admin [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| GET | `/terraform-providers/{terraformProviderId}` | `terraformProvider` (Q) | Details of a single Terraform provider. [G] | returns `TerraformProvider` [G] |
| PUT | `/terraform-providers/{terraformProviderId}` | `terraformProviderUpdate` (M) | Updates an existing Terraform Provider. [G] | body `TerraformProviderUpdateRequest`; returns `TerraformProvider` [G]; RBAC `TERRAFORM_PROVIDER_UPDATE` [I] by name, fallback Admin [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| DELETE | `/terraform-providers/{terraformProviderId}` | `terraformProviderDelete` (M) | Deletes a Terraform Provider. [G] | returns `TerraformProvider` [G]; RBAC `TERRAFORM_PROVIDER_DELETE` [I] by name, fallback Admin [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |
| POST | `/terraform-providers/{terraformProviderId}/set-visibility` | `terraformProviderSetVisibility` (M) | Changes public visibility of the Terraform Provider. [G] | body `TerraformProviderSetVisibilityRequest`; returns `TerraformProvider` [G]; RBAC `TERRAFORM_PROVIDER_SET_VISIBILITY` [I] by name, fallback Admin [G][D]; Tier `TERRAFORM_PROVIDER_VERSIONS` [I]; Business and above [[D]](https://docs.spacelift.io/vendors/terraform/provider-registry) |

### Terraform Providers: Fields of `TerraformProvider` (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/terraform-providers/{terraformProviderId}/version` | `version` on `TerraformProvider` | Get a particular version by its semver number. [G] | query `number`; returns `TerraformProviderVersion` [G] |

### 7.2 GPG Keys

Keys that sign registry artifacts **[B]**.

### GPG Keys (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/gpg-keys` | `gpgKeys` (Q) | List the GPG keys used to sign binaries. [G] | returns `[GPGKey]` [G] |
| POST | `/gpg-keys` | `gpgKeyCreate` (M) | Creates a new GPG key to be used for signing binaries. [G] | body `GpgKeyCreateRequest`; returns `GPGKey` [G] |
| PUT | `/gpg-keys/{gpgKeyId}` | `gpgKeyUpdate` (M) | Updates the description on the GPG key. [G] | body `GpgKeyUpdateRequest`; returns `GPGKey` [G] |
| POST | `/gpg-keys/{gpgKeyId}/revoke` | `gpgKeyRevoke` (M) | Revokes a GPG key to be used for signing binaries. [G] | returns `GPGKey` [G] |

### 7.3 Version Catalog

The Terraform, OpenTofu, Terragrunt and kubectl versions on offer, and the
effective version a constraint resolves to **[B]**
[[D]](https://docs.spacelift.io/vendors/terraform/version-management).

### Version Catalog (7)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/kubectl-versions` | `kubectlVersions` (Q) | List of all supported kubectl versions. [G] | returns `[String]` [G] |
| GET | `/open-tofu-effective-version` | `openTofuEffectiveVersion` (Q) | Given version constraints, calculate the latest version that satisfies it. [G] | query `constraints`; returns `String` [G] |
| GET | `/open-tofu-versions` | `openTofuVersions` (Q) | List of all supported OpenTofu versions. [G] | returns `[String]` [G] |
| GET | `/terraform-effective-version` | `terraformEffectiveVersion` (Q) | Given version constraints, calculate the latest version that satisfies it. [G] | query `constraints`; returns `String` [G] |
| GET | `/terraform-versions` | `terraformVersions` (Q) | List of all supported Terraform versions. [G] | returns `[String]` [G] |
| GET | `/terragrunt-effective-version` | `terragruntEffectiveVersion` (Q) | Given version constraints, calculate the latest version that satisfies it. NOTE: the only valid values for the "tool" parameter are TERRAFORM_FOSS or OPEN_TOFU, and when no tool is specified it defaults to TERRAFORM_FOSS. [G] | query `terragruntConstraints`, `terraformConstraints`, `tool`; returns `EffectiveTerragruntVersionResult` [G] |
| GET | `/terragrunt-versions` | `terragruntVersions` (Q) | List of all supported Terragrunt versions. [G] | returns `[String]` [G] |

### 7.4 OpenTofu Migration

Moving Terraform stacks to OpenTofu in bulk: check features, search what can
move, enqueue, follow the queue **[B]**
[[D]](https://docs.spacelift.io/vendors/opentofu/migration). The enqueue and
queue clean are untagged (section 14).

| Value | Description [G] |
|---|---|
| `TO_MIGRATE` | Queued for migration to native OpenTofu vendor. |
| `MIGRATED` | Successfully migrated to native OpenTofu vendor. |
| `TO_ROLLBACK` | Queued for rollback to legacy Terraform vendor. |
| `ROLLEDBACK` | Successfully rolled back to legacy Terraform vendor. |
| `FAILED` | Migration or rollback failed. See issues for details. Resume by re-issuing openTofuMigrate that matches this stack. |

| Value | Description [G] |
|---|---|
| `CONCISE_PLAN` |   |
| `CONCISE` |   |
| `STATE_ENCRYPTION` |   |
| `OTEL_TRACING` |   |
| `CPU_PROFILING` |   |
| `EPHEMERAL_RESOURCES` |   |
| `JSON_INTO` |   |

### OpenTofu Migration (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/open-tofu-features` | `openTofuFeatures` (Q) | Returns all OpenTofu features and whether they are supported for the given version constraint. [G] | query `version`; returns `[OpenTofuFeatureCheck]` [G] |
| GET | `/open-tofu-migrate-search` | `openTofuMigrateSearch` (Q) | Returns a paginated list of stacks eligible for OpenTofu vendor migration or rollback. This is the "preview" counterpart to openTofuMigrate: same eligible cohort and same predicate surface, but returns the matching stacks instead of enqueueing them. [G] | query `input`, `target`; returns `SearchStacksOutput` [G] |
| GET | `/open-tofu-migration-counts` | `openTofuMigrationCounts` (Q) | Returns the headline counters for the Terraform -> OpenTofu migration, for every stack in the account the caller can see. Meant for the migration dashboard: one call instead of four counting searches. [G] | returns `OpenTofuMigrationCounts` [G] |
| GET | `/open-tofu-migration-queue-search` | `openTofuMigrationQueueSearch` (Q) | Searches the OpenTofu migration queue (opentofu_migration_stacks table). Powers the dashboard widget, per-stack migration status checks, and the in-progress UI. [G] | query `input`; returns `OpenTofuMigrationQueueSearchOutput` [G] |

### 7.5 Tofu Workspaces

OpenTofu workspaces inside stacks, with unlock for a workspace left locked
**[B]**.

### Tofu Workspaces (3)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/stacks/{stackId}/tofu-workspaces/unlock` | `tofuWorkspaceUnlockAll` (M) | Unlock all locked Tofu workspaces in a stack. [G] | returns `TofuWorkspaceUnlockAllResponse` [G]; RBAC `STACK_WORKSPACE_UNLOCK` [I] by name, fallback Admin [G][D] |
| POST | `/tofu-workspaces/search` | `searchTofuWorkspaces` (Q) | Search Tofu Workspaces. [B] | body `SearchInput`; returns `SearchTofuWorkspacesOutput` [G] |
| POST | `/tofu-workspaces/{tofuWorkspaceId}/unlock` | `tofuWorkspaceUnlock` (M) | Unlock a single locked Tofu workspace. [G] | returns `TofuWorkspace` [G]; RBAC `STACK_WORKSPACE_UNLOCK` [I] by name, fallback Admin [G][D] |

## 8. Stack-adjacent features

### 8.1 Drift Detection

A per-stack schedule of proposed runs that detect drift and can reconcile it
(`DriftDetectionIntegrationInput`: `reconcile`, `ignoreState`, `timezone`,
`schedule`) **[G]**. Starter+ and above
[[D]](https://docs.spacelift.io/concepts/stack/drift-detection). There is no
list operation; the configuration comes back on the stack **[B]**. Report 18
records scheduling.

### Drift Detection (3)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/stacks/{stackId}/drift-detection` | `stackIntegrationDriftDetectionCreate` (M) | Create a new DriftDetectionIntegration for stack. [G] | body `DriftDetectionIntegrationInput`; returns `DriftDetectionIntegration` [G]; RBAC `DRIFT_DETECTION_INTEGRATION_CREATE` [I] by name, fallback Admin [G][D]; Tier `DRIFT_DETECTION` [I]; Starter+ and above [[D]](https://docs.spacelift.io/concepts/stack/drift-detection) |
| PUT | `/stacks/{stackId}/drift-detection` | `stackIntegrationDriftDetectionUpdate` (M) | Update DriftDetectionIntegration for stack. [G] | body `DriftDetectionIntegrationInput`; returns `DriftDetectionIntegration` [G]; RBAC `DRIFT_DETECTION_INTEGRATION_UPDATE` [I] by name, fallback Admin [G][D]; Tier `DRIFT_DETECTION` [I]; Starter+ and above [[D]](https://docs.spacelift.io/concepts/stack/drift-detection) |
| DELETE | `/stacks/{stackId}/drift-detection` | `stackIntegrationDriftDetectionDelete` (M) | Delete DriftDetectionIntegration for stack. [G] | returns `DriftDetectionIntegration` [G]; RBAC `DRIFT_DETECTION_INTEGRATION_DELETE` [I] by name, fallback Admin [G][D]; Tier `DRIFT_DETECTION` [I]; Starter+ and above [[D]](https://docs.spacelift.io/concepts/stack/drift-detection) |

### 8.2 Ansible

Hosts and task executions discovered by Ansible stacks, read-only **[B]**
[[D]](https://docs.spacelift.io/vendors/ansible).

| Value | Description [G] |
|---|---|
| `OK` |   |
| `CHANGED` |   |
| `SKIPPED` |   |
| `IGNORED` |   |
| `RESCUED` |   |
| `UNREACHABLE` |   |
| `FAILED` |   |

### Ansible (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/ansible-hosts` | `ansibleHosts` (Q) | Returns a list of related Ansible Hosts. [G] | query `input`; returns `[AnsibleHost]` [G] |
| POST | `/ansible-hosts/search` | `searchAnsibleHosts` (Q) | searchAnsibleHosts allows to search ansibleHosts. [G] | body `SearchInput`; returns `SearchAnsibleHostsOutput` [G] |
| GET | `/ansible-hosts/{ansibleHostId}` | `ansibleHost` (Q) | Individual AnsibleHost by ID. [G] | returns `AnsibleHost` [G] |
| GET | `/ansible-tasks` | `ansibleTasks` (Q) | Returns a list of related Ansible Tasks. [G] | query `input`; returns `[AnsibleTask]` [G] |
| POST | `/ansible-tasks/search` | `searchAnsibleTasks` (Q) | searchAnsibleTasks allows to search AnsibleTasks. [G] | body `SearchInput`; returns `SearchAnsibleTasksOutput` [G] |

### Ansible: Fields of `AnsibleHost` (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/ansible-hosts/{ansibleHostId}/search-ansible-task-executions` | `searchAnsibleTaskExecutions` on `AnsibleHost` | searchAnsibleTaskExecutions allows to search ansibleTaskExecutions of an ansibleHost. [G] | query `input`; returns `SearchAnsibleTaskExecutionsOutput` [G] |
| GET | `/ansible-hosts/{ansibleHostId}/search-ansible-task-executions-suggestions` | `searchAnsibleTaskExecutionsSuggestions` on `AnsibleHost` | Information about fields available for searching AnsibleTaskExecution. [G] | query `input`; returns `SearchSuggestionsOutput` [G] |

### 8.3 Resources

The account-wide resource inventory derived from run state, grouped by
provider, stack or type **[B]** [[D]](https://docs.spacelift.io/concepts/resources).
Per-stack variants are fields of `Stack` in report 03.

### Resources (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/managed-entities-group-by-providers/search` | `searchManagedEntitiesGroupByProviders` (Q) | Search managed entities grouped by providers with a set of predicates, ordering and paging. [G] | body `SearchInput`; returns `SearchManagedEntitiesGroupedByProvidersOutput` [G] |
| POST | `/managed-entities-group-by-stacks/search` | `searchManagedEntitiesGroupByStacks` (Q) | Search managed entities grouped by stacks with a set of predicates, ordering and paging. [G] | body `SearchInput`; returns `SearchManagedEntitiesGroupedByStacksOutput` [G] |
| POST | `/managed-entities-group-by-types/search` | `searchManagedEntitiesGroupByTypes` (Q) | Search managed entities grouped by types with a set of predicates, ordering and paging. [G] | body `SearchInput`; returns `SearchManagedEntitiesGroupedByTypesOutput` [G] |
| POST | `/managed-entities/search` | `searchManagedEntities` (Q) | Search managed entities with a set of predicates, ordering and paging. [G] | body `SearchInput`; returns `SearchManagedEntitiesOutput` [G] |

### 8.4 Scans

Scan definitions over cloud infrastructure, their runs and results; AWS is
the only `ScanProvider` **[G]**. No docs page in the mirror describes scans
*(unverified)*.

| Value | Description [G] |
|---|---|
| `QUEUED` |   |
| `SCANNING` |   |
| `FINISHED` |   |
| `FAILED` |   |
| `CANCELED` |   |
| `STOPPED` |   |
| `FINISHED_WITH_ERRORS` |   |

### Scans (11)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/scan-provider-schemas` | `scanProviderSchemas` (Q) | Providers a Scan can target, with the versions and resource types available for each. Anything absent here cannot be scanned. Pass a provider to get just that one. [G] | query `provider`; returns `[ScanProviderSchema]` [G] |
| GET | `/scan-runs/{scanRunId}` | `scanRun` (Q) | Details of a single ScanRun. [G] | returns `ScanRun` [G] |
| POST | `/scan-runs/{scanRunId}/cancel` | `scanRunCancel` (M) | Cancel a queued Scan run before a worker picks it up. [G] | body `ScanRunCancelRequest`; returns `ScanRun` [G]; Tier `SCANS` [I] |
| POST | `/scan-runs/{scanRunId}/stop` | `scanRunStop` (M) | Ask the worker running a Scan run to abandon it. The run becomes STOPPED once the worker reports back. [G] | body `ScanRunStopRequest`; returns `ScanRun` [G]; Tier `SCANS` [I] |
| POST | `/scans` | `scanCreate` (M) | Create a new Scan configuration. [G] | body `ScanInput`; returns `Scan` [G]; Tier `SCANS` [I] |
| POST | `/scans/search` | `searchScans` (Q) | Search Scans with a set of predicates, ordering and paging. [G] | body `SearchInput`; returns `SearchScansOutput` [G] |
| POST | `/scans/search/suggestions` | `searchScansSuggestions` (Q) | Information about fields available for searching Scans. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/scans/{scanId}` | `scan` (Q) | Details of a single Scan. [G] | returns `Scan` [G] |
| PUT | `/scans/{scanId}` | `scanUpdate` (M) | Update an existing Scan configuration. [G] | body `ScanInput`; returns `Scan` [G]; Tier `SCANS` [I] |
| DELETE | `/scans/{scanId}` | `scanDelete` (M) | Delete a Scan configuration. [G] | returns `Scan` [G]; Tier `SCANS` [I] |
| POST | `/scans/{scanId}/runs/trigger` | `scanRunTrigger` (M) | Queue a new run of an existing Scan. [G] | returns `ScanRun` [G]; Tier `SCANS` [I] |

### Scans: Fields of `ScanRun` (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/scan-runs/{scanRunId}/search-failures` | `searchFailures` on `ScanRun` | The run's failures, with a set of predicates, ordering and paging. [G] | query `input`; returns `SearchScanRunFailuresOutput` [G] |
| GET | `/scan-runs/{scanRunId}/search-failures-suggestions` | `searchFailuresSuggestions` on `ScanRun` | Information about fields available for searching this run's failures. [G] | query `input`; returns `SearchSuggestionsOutput` [G] |
| GET | `/scan-runs/{scanRunId}/search-resources` | `searchResources` on `ScanRun` | The resources this run discovered, with a set of predicates, ordering and paging. [G] | query `input`; returns `SearchScanResourcesOutput` [G] |
| GET | `/scan-runs/{scanRunId}/search-resources-suggestions` | `searchResourcesSuggestions` on `ScanRun` | Information about fields available for searching this run's resources. [G] | query `input`; returns `SearchSuggestionsOutput` [G] |

### Scans: Fields of `Scan` (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/scans/{scanId}/search-runs` | `searchRuns` on `Scan` | The scan's runs, with a set of predicates, ordering and paging. [G] | query `input`; returns `SearchScanRunsOutput` [G]; Account-wide, newest first; window via a `TimeInRange` predicate on `createdAt` [G] |
| GET | `/scans/{scanId}/search-runs-suggestions` | `searchRunsSuggestions` on `Scan` | Information about fields available for searching this scan's runs. [G] | query `input`; returns `SearchSuggestionsOutput` [G] |

## 9. Templates and blueprints

### 9.1 Templates

The successor to blueprints: a versioned, parameterized stack definition and
the deployments made from it **[B]**. Business and above
[[D]](https://docs.spacelift.io/concepts/template). The RBAC catalog still
names these actions `BLUEPRINT` **[G][D]**. Report 17 records the model.

| Value | Description [G] |
|---|---|
| `NONE` |   |
| `FINISHED` |   |
| `IN_PROGRESS` |   |
| `FAILURE` |   |
| `DESTROYING` |   |
| `DESTROYING_FAILED` |   |
| `UNCONFIRMED` |   |
| `DISCARDED` |   |

| Value | Description [G] |
|---|---|
| `REGULAR` |   |
| `TEST` |   |

### Templates (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/templates` | `templateCreate` (M) | Create Template. [B] | body `TemplateCreateInput`; returns `Template` [G]; RBAC `CREATE_BLUEPRINTS_VERSIONED_GROUP` [I] by name, fallback Admin [G][D]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |
| POST | `/templates/search` | `searchTemplates` (Q) | Search Templates. [B] | body `SearchInput`; returns `SearchTemplatesOutput` [G] |
| POST | `/templates/search/suggestions` | `searchTemplatesSuggestions` (Q) | Search Templates Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/templates/{templateId}` | `template` (Q) | Returns a single template details. [G] | returns `Template` [G] |
| PUT | `/templates/{templateId}` | `templateUpdate` (M) | Update Template. [B] | body `TemplateUpdateInput`; returns `Template` [G]; RBAC `UPDATE_BLUEPRINTS_VERSIONED_GROUP` [I] by name, fallback Admin [G][D]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |
| DELETE | `/templates/{templateId}` | `templateDelete` (M) | Delete Template. [B] | returns `Template` [G]; RBAC `DELETE_BLUEPRINTS_VERSIONED_GROUP` [I] by name, fallback Admin [G][D]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |

### Templates: Deployments (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/template-deployments/search` | `searchTemplateDeployments` (Q) | Search Deployments. [B] | body `SearchInput`; returns `SearchTemplateDeploymentsOutput` [G] |
| POST | `/templates/{templateId}/deployments` | `templateDeploymentCreate` (M) | Deploy a template. [G] | body `TemplateDeploymentCreateInput`; returns `TemplateDeployment` [G]; RBAC `CREATE_BLUEPRINT_DEPLOYMENT` [I] by name, fallback Admin; RBAC `TEST_BLUEPRINT_DEPLOYMENT` [I] by name, fallback Admin [G][D]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |
| GET | `/templates/{templateId}/deployments/{deploymentId}` | `templateDeployment` (Q) | Details of a single TemplateDeployment. [G] | returns `TemplateDeployment` [G] |
| PUT | `/templates/{templateId}/deployments/{deploymentId}` | `templateDeploymentUpdate` (M) | Upgrade a template deployment to a new version and update deployment metadata. [G] | body `TemplateDeploymentUpdateInput`; returns `TemplateDeployment` [G]; RBAC `UPGRADE_BLUEPRINT_DEPLOYMENT` [I] by name, fallback Admin [G][D]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |
| DELETE | `/templates/{templateId}/deployments/{deploymentId}` | `templateDeploymentDelete` (M) | Delete a template deployment. If skipStackDeletion is true, the deployment record is removed and stacks are detached (made standalone) instead of being destroyed. [G] | query `skipStackDeletion`; returns `TemplateDeployment` [G]; RBAC `DELETE_BLUEPRINT_DEPLOYMENT` [I] by name, fallback Admin [G][D]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |
| POST | `/templates/{templateId}/deployments/{deploymentId}/rollback` | `templateDeploymentRollback` (M) | Rollback a template deployment to a previous state from its change history. [G] | body `TemplateDeploymentRollbackRequest`; returns `TemplateDeployment` [G]; RBAC `ROLLBACK_BLUEPRINT_DEPLOYMENT` [I] by name, fallback Admin [G][D]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |

### Templates: Versions (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/template-version-parse-template` | `templateVersionParseTemplate` (Q) | Parse a template and return its inputs and any validation errors. [G] | query `template`; returns `BlueprintParseResult` [G] |
| POST | `/template-versions` | `templateVersionCreate` (M) | Create Version. [B] | body `TemplateVersionCreateInput`; returns `TemplateVersion` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |
| PUT | `/template-versions/{templateVersionId}` | `templateVersionUpdate` (M) | Update Version. [B] | body `TemplateVersionUpdateInput`; returns `TemplateVersion` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |
| DELETE | `/template-versions/{templateVersionId}` | `templateVersionDelete` (M) | Delete Version. [B] | returns `TemplateVersion` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |
| POST | `/template-versions/{templateVersionId}/deprecate` | `templateVersionDeprecate` (M) | Mark a published template version as deprecated. [G] | body `TemplateVersionDeprecateInput`; returns `TemplateVersion` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/template) |

### Templates: Fields of `TemplateDeployment` (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/templates/{templateId}/deployments/{deploymentId}/search-change-history` | `searchChangeHistory` on `TemplateDeployment` | Search through the history of changes made to this deployment. [G] | query `input`; returns `SearchTemplateDeploymentChangeHistoryOutput` [G] |

### Templates: Fields of `Template` (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/templates/{templateId}/compatible-versions` | `compatibleVersions` on `Template` | Get template versions that are compatible upgrade targets from the specified version. Returns only PUBLISHED versions that can be upgraded to from the given version. [G] | query `versionId`; returns `[TemplateVersion]` [G] |
| GET | `/templates/{templateId}/search-template-versions` | `searchTemplateVersions` on `Template` | Search for template versions. [G] | query `input`; returns `SearchTemplateVersionsOutput` [G] |
| GET | `/templates/{templateId}/search-template-versions-suggestions` | `searchTemplateVersionsSuggestions` on `Template` | Get search suggestions for template versions. [G] | query `input`; returns `SearchSuggestionsOutput` [G] |
| GET | `/templates/{templateId}/template-version` | `templateVersion` on `Template` | Get a single template version by ID (slug or ULID). [G] | query `id`; returns `TemplateVersion` [G] |
| GET | `/templates/{templateId}/template-version-by-number` | `templateVersionByNumber` on `Template` | Get a single template version by its version number. [G] | query `versionNumber`; returns `TemplateVersion` [G] |

### 9.2 Blueprints

The older single-version blueprints; many of their operations are deprecated
in favour of templates and excluded from the reference (`coverage.md`)
**[G]**. Blueprints have no RBAC actions; the docs restrict them to space
admins [[D]](https://docs.spacelift.io/concepts/authorization/rbac-system).

| Value | Description [G] |
|---|---|
| `UNKNOWN` | Invalid "zero value" for the type. |
| `DRAFT` | Blueprint is in draft state. |
| `PUBLISHED` | Blueprint is in published state. |
| `DELETED` | The blueprint is deleted. |

### Blueprints (10)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/blueprint-schema` | `blueprintSchema` (Q) | The json schema of a blueprint. [G] | returns `String` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| POST | `/blueprints` | `blueprintCreate` (M) | Create a blueprint. [G] | body `BlueprintCreateInput`; returns `Blueprint` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| POST | `/blueprints/parse-template` | `blueprintParseTemplate` (M) | Parse Template. [B] | body `BlueprintParseTemplateRequest`; returns `BlueprintParseResult` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| POST | `/blueprints/search` | `searchBlueprints` (Q) | List Blueprints. [B] | body `SearchInput`; returns `SearchBlueprintsOutput` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| GET | `/blueprints/{blueprintId}` | `blueprint` (Q) | Returns a single blueprint. [G] | returns `Blueprint` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| PUT | `/blueprints/{blueprintId}` | `blueprintUpdate` (M) | Update an existing blueprint. [G] | body `BlueprintCreateInput`; returns `Blueprint` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| DELETE | `/blueprints/{blueprintId}` | `blueprintDelete` (M) | Delete an existing blueprint. [G] | returns `Blueprint` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| POST | `/blueprints/{blueprintId}/clone` | `blueprintClone` (M) | Clone Blueprint. [B] | body `BlueprintCloneInput`; returns `Blueprint` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| POST | `/blueprints/{blueprintId}/create-stack` | `blueprintCreateStack` (M) | Create Stack from Blueprint. [B] | body `BlueprintStackCreateInput`; returns `BlueprintStackCreation` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| GET | `/template-schema` | `templateSchema` (Q) | The json schema of a template. [G] | returns `String` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |

### Blueprints: Deployments (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/blueprints/{blueprintId}/attach-to-integration` | `blueprintAttachToIntegration` (M) | Attach to Integration. [B] | body `BlueprintAttachInput`; returns `BlueprintIntegrationAttachment` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| POST | `/blueprints/{blueprintId}/detach-integration` | `blueprintDetachIntegration` (M) | Detach Integration. [B] | returns `BlueprintIntegrationAttachment` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |

### Blueprints: Groups (3)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/blueprint-with-groups` | `blueprintWithGroupCreate` (M) | Create a blueprint. [G] | body `BlueprintWithGroupCreateInput`; returns `Blueprint` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| PUT | `/blueprint-with-groups/{blueprintWithGroupId}` | `blueprintWithGroupUpdate` (M) | Update an existing blueprint. [G] | body `BlueprintWithGroupUpdateInput`; returns `Blueprint` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |
| DELETE | `/blueprint-with-groups/{blueprintWithGroupId}` | `blueprintWithGroupDelete` (M) | Delete an existing blueprint. [G] | returns `Blueprint` [G]; Tier `BLUEPRINTS` [I]; Business and above [[D]](https://docs.spacelift.io/concepts/blueprint) |

## 10. Intelligence

### 10.1 AI Integrations

Connections to AI providers behind Spacelift Intelligence; the
Spacelift-provided integration is managed and cannot be updated **[G]**
[[D]](https://docs.spacelift.io/concepts/intelligence/ai-integrations).

### AI Integrations (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/ai-integrations` | `aiIntegrations` (Q) | Returns all AI integrations for the current account. [G] | returns `[AIIntegration]` [G] |
| POST | `/ai-integrations` | `aiIntegrationCreate` (M) | Create an AI integration. [G] | body `AiIntegrationCreateRequest`; returns `AIIntegration` [G]; RBAC `AI_INTEGRATION_CREATE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I] |
| GET | `/ai-integrations/{aiIntegrationId}` | `aiIntegration` (Q) | Returns all AI integrations for the current account. [G] | returns `AIIntegration` [G] |
| PUT | `/ai-integrations/{aiIntegrationId}` | `aiIntegrationUpdate` (M) | Update an AI integration. Spacelift-provided integrations are managed and cannot be updated through this mutation. [G] | body `AiIntegrationUpdateRequest`; returns `AIIntegration` [G]; RBAC `AI_INTEGRATION_UPDATE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I] |
| DELETE | `/ai-integrations/{aiIntegrationId}` | `aiIntegrationDelete` (M) | Delete an AI integration. [G] | returns `AIIntegration` [G]; RBAC `AI_INTEGRATION_DELETE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I] |

### 10.2 Intent

Intent projects manage cloud resources directly rather than through stack
code, with resource operations reviewed against intent policies, chat
conversations for the assistant, and conversion to a stack
(`stackCreateFromIntent`) [[D]](https://docs.spacelift.io/concepts/intelligence/intent).
Most operations have no schema description; Bruno marks them a preview
feature **[B]**. The RBAC catalog has resource create, update, delete,
import, refresh and resume actions that no GraphQL mutation in the reference
carries **[G]**; report 22 covers the MCP tools that perform them.

| Value | Description [G] |
|---|---|
| `UNKNOWN` | An invalid "zero-value" for the intent resource operation state. |
| `FAILED` | The operation has failed. |
| `FINISHED` | The operation has finished successfully. |
| `PENDING_REVIEW` | The operation is pending for manual allow/deny. |
| `READY_FOR_EXECUTION` | The operation has reviewed and ready for execution. |
| `PENDING` | The operation is pending execution. |

### Intent (33)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/intent-chat-conversations` | `intentChatConversations` (Q) | Intent Chat Conversations. [B] | query `page`, `perPage`, `search`; returns `[IntentChatConversation]` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-chat-conversations` | `intentChatConversationCreate` (M) | Intent Chat Conversation Create. [B] | returns `IntentChatConversation` [G]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| GET | `/intent-chat-conversations/{intentChatConversationId}` | `intentChatConversation` (Q) | Intent Chat Conversation. [B] | returns `IntentChatConversation` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| PUT | `/intent-chat-conversations/{intentChatConversationId}` | `intentChatConversationUpdate` (M) | Intent Chat Conversation Update. [B] | body `IntentChatConversationUpdateRequest`; returns `IntentChatConversation` [G]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| DELETE | `/intent-chat-conversations/{intentChatConversationId}` | `intentChatConversationDelete` (M) | Intent Chat Conversation Delete. [B] | returns `IntentChatConversation` [G]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| GET | `/intent-module-resources` | `intentModuleResources` (Q) | Returns the underlying terraform-managed resources for a spacelift/module intent resource by walking its terraform state. Data sources are excluded. Cursor-paginated with first/after; at most 50 resources per page. [G] | query `intentProjectID`, `resourceID`, `first`, `after`; returns `IntentModuleResourcesOutput` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| GET | `/intent-policy-input-schema` | `intentPolicyInputSchema` (Q) | Returns the input schema that intent policies receive for evaluation. [G] | returns `String` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| GET | `/intent-project-active` | `intentProjectActive` (Q) | Intent Project Active. [B] | returns `IntentProject` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| GET | `/intent-projects` | `intentProjects` (Q) | Intent Projects. [B] | returns `[IntentProject]` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects` | `intentProjectCreate` (M) | Intent Project Create. [B] | body `IntentProjectCreateRequest`; returns `IntentProject` [G]; RBAC `INTENT_PROJECT_CREATE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/search` | `searchIntentProjects` (Q) | Search Intent Projects. [B] | body `SearchInput`; returns `SearchIntentProjectsOutput` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/search/suggestions` | `searchIntentProjectsSuggestions` (Q) | Search Intent Projects Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| GET | `/intent-projects/{intentProjectId}` | `intentProject` (Q) | Intent Project. [B] | returns `IntentProject` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| PUT | `/intent-projects/{intentProjectId}` | `intentProjectUpdate` (M) | Intent Project Update. [B] | body `IntentProjectUpdateRequest`; returns `IntentProject` [G]; RBAC `INTENT_PROJECT_UPDATE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| DELETE | `/intent-projects/{intentProjectId}` | `intentProjectDelete` (M) | Intent Project Delete. [B] | query `keepResources`; returns `IntentProject` [G]; RBAC `INTENT_PROJECT_DELETE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/activate` | `intentProjectActivate` (M) | Intent Project Activate. [B] | returns `IntentProject` [G]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/configs` | `intentProjectConfigAdd` (M) | Intent Project Config Add. [B] | body `ConfigInput`; returns `ConfigElement` [G]; RBAC `INTENT_PROJECT_CONFIG_ADD` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| PUT | `/intent-projects/{intentProjectId}/configs/{configId}` | `intentProjectConfigUpdate` (M) | Intent Project Config Update. [B] | body `ConfigInputBody`; returns `ConfigElement` [G]; RBAC `INTENT_PROJECT_CONFIG_UPDATE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| DELETE | `/intent-projects/{intentProjectId}/configs/{configId}` | `intentProjectConfigDelete` (M) | Intent Project Config Delete. [B] | returns `ConfigElement` [G]; RBAC `INTENT_PROJECT_CONFIG_DELETE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/disable` | `intentProjectDisable` (M) | Intent Project Disable. [B] | returns `IntentProject` [G]; RBAC `INTENT_PROJECT_DISABLE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/enable` | `intentProjectEnable` (M) | Intent Project Enable. [B] | returns `IntentProject` [G]; RBAC `INTENT_PROJECT_ENABLE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/intent-resource-dependencies` | `intentResourceDependencyAdd` (M) | Intent Resource Dependency Add. [B] | body `IntentResourceDependencyAddRequest`; returns `IntentResourceDependency` [G]; RBAC `INTENT_DEPENDENCIES_ADD` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| DELETE | `/intent-projects/{intentProjectId}/intent-resource-dependencies/{fromResourceId}/{toResourceId}` | `intentResourceDependencyRemove` (M) | Intent Resource Dependency Remove. [B] | returns `IntentResourceDependency` [G]; RBAC `INTENT_DEPENDENCIES_REMOVE` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/intent-resource-operations/{operationId}/review` | `intentResourceOperationReview` (M) | Intent Resource Operation Review. [B] | body `IntentResourceOperationReviewRequest`; returns `IntentResourceOperationReview` [G]; RBAC `INTENT_RESOURCE_OPERATION_REVIEW` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/intent-resources/{resourceId}/eject` | `intentResourceStateEject` (M) | Intent Resource State Eject. [B] | returns `IntentResource` [G]; RBAC `INTENT_STATE_EJECT` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/stacks` | `stackCreateFromIntent` (M) | Create a new stack based on existing Intent Project. [G] | body `StackCreateFromIntentRequest`; returns `Stack` [G]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-projects/{intentProjectId}/unlock` | `intentProjectUnlock` (M) | Intent Project Unlock. [B] | returns `IntentProject` [G]; RBAC `INTENT_PROJECT_UNLOCK` [I] by name, fallback Admin [G][D]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| PUT | `/intent-projects/{intentProjectId}/worker-pool` | `intentProjectAttachWorkerPool` (M) | Intent Project Attach Worker Pool. [B] | body `IntentProjectAttachWorkerPoolRequest`; returns `IntentProject` [G]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| DELETE | `/intent-projects/{intentProjectId}/worker-pool` | `intentProjectDetachWorkerPool` (M) | Intent Project Detach Worker Pool. [B] | returns `IntentProject` [G]; Tier `SPACELIFT_INTELLIGENCE` [I]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-resources-operations/search` | `searchIntentResourcesOperations` (Q) | Search Intent Resources Operations. [B] | body `SearchInput`; returns `SearchIntentResourcesOperationsOutput` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-resources-operations/search/suggestions` | `searchIntentResourcesOperationsSuggestions` (Q) | Search Intent Resources Operations Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-resources/search` | `searchIntentResources` (Q) | Search Intent Resources. [B] | body `SearchInput`; returns `SearchIntentResourcesOutput` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |
| POST | `/intent-resources/search/suggestions` | `searchIntentResourcesSuggestions` (Q) | Search Intent Resources Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: intent and ai chat preview feature [B] |

### Intent: Fields of `IntentChatConversation` (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/intent-chat-conversations/{intentChatConversationId}/transcript` | `transcript` on `IntentChatConversation` | Markdown transcript of the conversation, for continuing it in an external agentic harness. [G] | query `offset`, `limit`; returns `String` [G] |

### Intent: Fields of `IntentProject` (6)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/intent-projects/{intentProjectId}/attachable-policies` | `attachablePolicies` on `IntentProject` | List of policies that can be attached to this project. [G] | returns `[Policy]` [G] |
| GET | `/intent-projects/{intentProjectId}/hcl` | `hcl` on `IntentProject` | HCL representation of the project with pagination support. page: Page number (1-based), defaults to 1 pageSize: Number of resources per page (1-500), defaults to 50. [G] | query `page`, `pageSize`; returns `IntentProjectHclResult` [G] |
| GET | `/intent-projects/{intentProjectId}/search-intent-resources` | `searchIntentResources` on `IntentProject` | Search intent resources with a set of predicates, ordering and paging. [G] | query `input`; returns `SearchIntentResourcesOutput` [G] |
| GET | `/intent-projects/{intentProjectId}/search-intent-resources-operations` | `searchIntentResourcesOperations` on `IntentProject` | Search intent resources operations with a set of predicates, ordering and paging. [G] | query `input`; returns `SearchIntentResourcesOperationsOutput` [G] |
| GET | `/intent-projects/{intentProjectId}/search-intent-resources-operations-suggestions` | `searchIntentResourcesOperationsSuggestions` on `IntentProject` | Information about fields available for searching intent resource operations. [G] | query `input`; returns `SearchSuggestionsOutput` [G] |
| GET | `/intent-projects/{intentProjectId}/search-intent-resources-suggestions` | `searchIntentResourcesSuggestions` on `IntentProject` | Information about fields available for searching intent resources. [G] | query `input`; returns `SearchSuggestionsOutput` [G] |

## 11. Flows

The link between a Spacelift account and a Spacelift Flows organization:
instances, the linked integration, projects, and the space role bindings on
them **[G]**. Flows' own API is separate (report 23).

### Flows (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/flows-instances` | `flowsInstances` (Q) | Returns all configured Flows instances available for linking. [G] | returns `[FlowsInstance]` [G] |
| GET | `/flows-integration` | `flowsIntegration` (Q) | Returns the Flows integration for the current account, if linked. [G] | returns `FlowsIntegration` [G] |
| POST | `/flows-project-role-binding-batches` | `flowsProjectRoleBindingBatchCreate` (M) | Assigns a batch of (space, role) bindings to Flows projects. Requires admin access to every target space. Returns the created bindings. [G] | body `FlowsProjectRoleBindingBatchInput`; returns `[FlowsProjectRoleBinding]` [G] |
| DELETE | `/flows-project-role-bindings/{flowsProjectRoleBindingId}` | `flowsProjectRoleBindingDelete` (M) | Removes a single Flows project role binding by its ID. [G] | returns `FlowsProjectRoleBinding` [G] |
| GET | `/flows-projects` | `flowsProjects` (Q) | Returns the projects of the linked Flows organization, each with the Space role bindings currently assigned to it. Empty when no Flows org is linked. [G] | returns `[FlowsProject]` [G] |

### Flows: Root fields with no Bruno request (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/flows-project-role-bindings/{flowsProjectRoleBindingId}` | `flowsProjectRoleBinding` (Q) | Returns a single Flows project role binding by its ID, or null if it does not exist. Requires admin access to the binding's space. [G] | returns `FlowsProjectRoleBinding` [G]; Admin on the binding's space [G] |

## 12. Plugins

Plugins installed from plugin templates, with their own lifecycle **[B]**
[[D]](https://docs.spacelift.io/integrations/plugins).

| Value | Description [G] |
|---|---|
| `NONE` |   |
| `STRING` |   |
| `BOOLEAN` |   |
| `NUMBER` |   |

### Plugins (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/plugins` | `plugins` (Q) | List of all the plugins in the account. [G] | returns `[Plugin]` [G] |
| POST | `/plugins` | `pluginInstall` (M) | Install a plugin from a plugin template. [G] | body `PluginInstallInput`; returns `Plugin` [G] |
| GET | `/plugins/{pluginId}` | `plugin` (Q) | Details of a single Plugin. [G] | returns `Plugin` [G] |
| PUT | `/plugins/{pluginId}` | `pluginUpdate` (M) | Update an existing Plugin. [G] | body `PluginUpdateInput`; returns `Plugin` [G] |
| DELETE | `/plugins/{pluginId}` | `pluginDelete` (M) | Delete an existing Plugin. [G] | returns `Plugin` [G] |

### Plugins: Templates (4)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/plugin-templates` | `pluginTemplates` (Q) | List of all the plugin templates in the account. [G] | returns `[PluginTemplate]` [G] |
| POST | `/plugin-templates` | `pluginTemplateCreate` (M) | Create a new Plugin Template. [G] | body `PluginTemplateCreateInput`; returns `PluginTemplate` [G] |
| GET | `/plugin-templates/{pluginTemplateId}` | `pluginTemplate` (Q) | Details of a single Plugin Template. [G] | returns `PluginTemplate` [G] |
| DELETE | `/plugin-templates/{pluginTemplateId}` | `pluginTemplateDelete` (M) | Delete an existing Plugin Template. [G] | returns `PluginTemplate` [G] |

## 13. Web UI support

These tags serve the web UI rather than infrastructure: saved list filters,
the filter-bar suggestions that pair with each `search…` query, a key/value
store for layout, guided tours and certifications, and in-app forms
**[B]**. Search suggestions are still the way to discover valid predicate
fields for a search **[B]**.

| Value | Description [G] |
|---|---|
| `BOOLEAN` |   |
| `ENUM` |   |
| `STRING` |   |
| `HIERARCHY` |   |
| `TIME` |   |

### Saved Filters (5)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/saved-filters` | `savedFilters` (Q) | List of saved filters. [G] | query `type`; returns `[SavedFilter]` [G] |
| POST | `/saved-filters` | `savedFilterCreate` (M) | Create a new saved filter. [G] | body `SavedFilterInput`; returns `SavedFilter` [G] |
| GET | `/saved-filters/{savedFilterId}` | `savedFilter` (Q) | Details of a single saved filter. [G] | returns `SavedFilter` [G] |
| PUT | `/saved-filters/{savedFilterId}` | `savedFilterUpdate` (M) | Update a saved filter. [G] | body `SavedFilterUpdateRequest`; returns `SavedFilter` [G] |
| DELETE | `/saved-filters/{savedFilterId}` | `savedFilterDelete` (M) | Delete a saved filter. [G] | returns `SavedFilter` [G] |

### Search Suggestions (30)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| POST | `/ansible-hosts/search/suggestions` | `searchAnsibleHostsSuggestions` (Q) | Information about fields available for searching AnsibleHosts. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/ansible-tasks/search/suggestions` | `searchAnsibleTasksSuggestions` (Q) | Information about fields available for searching AnsibleTasks. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/api-key-sessions-with-access-to-space-suggestions` | `apiKeySessionsWithAccessToSpaceSuggestions` (Q) | Returns the available search fields and their possible values for apiKeySessionsWithAccessToSpace. [G] | query `space`, `input`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/api-keys-with-access-to-space-suggestions` | `apiKeysWithAccessToSpaceSuggestions` (Q) | Returns the available search fields and their possible values for apiKeysWithAccessToSpace. [G] | query `space`, `input`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/audit-trail-entries/search/suggestions` | `searchAuditTrailEntriesSuggestions` (Q) | Information about fields available for searching audit trail entries. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/blueprints/search/suggestions` | `searchBlueprintsSuggestions` (Q) | Search Blueprints Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/contexts/search/suggestions` | `searchContextsSuggestions` (Q) | Search Contexts Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/managed-entities/search/suggestions` | `searchManagedEntitiesSuggestions` (Q) | Information about fields available for searching managed entities. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/modules/search/suggestions` | `searchModulesSuggestions` (Q) | Information about fields available for searching Modules. [G] | body `SearchModulesSuggestionsRequest`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/named-webhooks-integrations/search/suggestions` | `searchNamedWebhooksIntegrationsSuggestions` (Q) | Search Named Webhooks Integrations Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/notifications/search/suggestions` | `searchNotificationsSuggestions` (Q) | Information about fields available for searching Notifications. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/open-tofu-migrate-suggestions` | `openTofuMigrateSuggestions` (Q) | Returns search suggestions (predicate fields and possible values) for stacks eligible for OpenTofu vendor migration or rollback. [G] | query `input`, `target`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/open-tofu-migration-queue-search-suggestions` | `openTofuMigrationQueueSearchSuggestions` (Q) | Returns search suggestions (predicate fields and possible values) for the OpenTofu migration queue. The suggestions counterpart of openTofuMigrationQueueSearch: same rows, same predicate surface (state, createdAt, updatedAt, stackName, stackSlug, space, label). [G] | query `input`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/policies/search/suggestions` | `searchPoliciesSuggestions` (Q) | Search Policies Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/policy-templates/search/suggestions` | `searchPolicyTemplatesSuggestions` (Q) | Search Policy Templates Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/repos/search/suggestions` | `searchReposSuggestions` (Q) | Get field suggestions for Repo search. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/repos/{repoId}/commits/search/suggestions` | `searchRepoCommitsSuggestions` (Q) | Get field suggestions for Repo commit search. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/repos/{repoId}/files/search/suggestions` | `searchRepoFilesSuggestions` (Q) | Get field suggestions for Repo file search. [G] | body `SearchRepoFilesSuggestionsRequest`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/runs/search/suggestions` | `searchRunsSuggestions` (Q) | searchRunsSuggestions returns the filterable fields and their possible values for the searchRuns query. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/space-contexts/search/suggestions` | `searchSpaceContextsSuggestions` (Q) | Filter-dropdown suggestions scoped to the given space's contexts. [G] | body `SearchSpaceContextsSuggestionsRequest`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/space-policies/search/suggestions` | `searchSpacePoliciesSuggestions` (Q) | Filter-dropdown suggestions scoped to the given space's policies. [G] | body `SearchSpacePoliciesSuggestionsRequest`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/spaces/search/suggestions` | `searchSpacesSuggestions` (Q) | Information about fields available for searching Spaces. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/stacks-with-access-to-space-suggestions` | `stacksWithAccessToSpaceSuggestions` (Q) | Returns the available search fields and their possible values for stacksWithAccessToSpace. [G] | query `space`, `input`, `mode`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/stacks/search/suggestions` | `searchStacksSuggestions` (Q) | Information about fields available for searching Stacks. [G] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/tofu-workspaces/search/suggestions` | `searchTofuWorkspacesSuggestions` (Q) | Search Tofu Workspaces Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/user-groups-with-access-to-space-suggestions` | `userGroupsWithAccessToSpaceSuggestions` (Q) | Returns the available search fields and their possible values for userGroupsWithAccessToSpace. [G] | query `space`, `input`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/user-sessions-with-access-to-space-suggestions` | `userSessionsWithAccessToSpaceSuggestions` (Q) | Returns the available search fields and their possible values for userSessionsWithAccessToSpace. [G] | query `space`, `input`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| GET | `/users-with-access-to-space-suggestions` | `usersWithAccessToSpaceSuggestions` (Q) | Returns the available search fields and their possible values for usersWithAccessToSpace. [G] | query `space`, `input`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/v-cs-integrations/search/suggestions` | `searchVCSIntegrationsSuggestions` (Q) | Search VCSIntegrations Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |
| POST | `/worker-pools/search/suggestions` | `searchWorkerPoolsSuggestions` (Q) | Search Worker Pools Suggestions. [B] | body `SearchSuggestionsInput`; returns `SearchSuggestionsOutput` [G]; Bruno marks it advanced: autocomplete helper that powers search filters in the ui [B] |

### UI State (2)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/ui-config` | `uiConfigGet` (Q) | Get the stored UI configuration for the user. If no configuration is stored, empty string will be returned. [G] | returns `String` [G]; Bruno marks it advanced: stores in-app ui preferences [B] |
| PUT | `/ui-config` | `uiConfigStore` (M) | Upload a persistent UI config. Consecutive calls will overwrite the previous config. UI config is a JSON blob. [G] | body `UiConfigStoreRequest`; returns `Boolean` [G]; Bruno marks it advanced: stores in-app ui preferences [B] |

### User Guide (18)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/active-user-guide-progress` | `activeUserGuideProgress` (Q) | Get the currently active user guide progress for the user. [G] | returns `UserGuideProgress` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| POST | `/active-user-guide-progress/abandon` | `userGuideAbandon` (M) | Abandon the active user guide. [G] | returns `UserGuideProgress` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| POST | `/active-user-guide-progress/complete` | `userGuideComplete` (M) | Complete the active user guide. [G] | returns `UserGuideProgress` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| POST | `/active-user-guide-progress/next-step` | `userGuideMoveToNextStep` (M) | Move to the next step for the active user guide. [G] | returns `UserGuideProgress` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| POST | `/active-user-guide-progress/previous-step` | `userGuideMoveToPreviousStep` (M) | Move to the previous step for the active user guide. [G] | returns `UserGuideProgress` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/completed-user-guides` | `completedUserGuides` (Q) | Retrieve all completed guides for the user. [G] | returns `[UserGuideProgress]` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/onboarding-progress` | `onboardingProgress` (Q) | Onboarding progress for all users on the account. [G] | returns `[OnboardingProgress]` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/user-guide-certification-list` | `userGuideCertificationList` (Q) | List the calling user's certificates. [G] | returns `[UserGuideCertification]` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| POST | `/user-guide-certifications` | `userGuideCertificationCreate` (M) | Creates a new certificate for completing a course. Returns an error if a certificate already exists for this user and group. [G] | body `UserGuideCertificationCreateRequest`; returns `UserGuideCertification` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| PUT | `/user-guide-certifications/{userGuideCertificationId}` | `userGuideCertificationUpdate` (M) | Updates the display name of an existing certificate. Returns an error if the certificate does not exist. [G] | body `UserGuideCertificationUpdateRequest`; returns `UserGuideCertification` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/user-guide-chapters` | `userGuideChapters` (Q) | List all user guide chapters. [G] | returns `[UserGuideChapter]` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/user-guide-chapters/{userGuideChapterId}` | `userGuideChapter` (Q) | Retrieve a single user guide chapter by ID. [G] | returns `UserGuideChapter` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/user-guide-groups` | `userGuideGroups` (Q) | List all user guide groups. [G] | returns `[UserGuideGroup]` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/user-guide-groups/{userGuideGroupId}` | `userGuideGroup` (Q) | Retrieve a single user guide group by ID. [G] | returns `UserGuideGroup` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/user-guides` | `userGuides` (Q) | List all available user guides. [G] | returns `[UserGuide]` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| GET | `/user-guides/{userGuideId}` | `userGuide` (Q) | Retrieve a single user guide by ID. [G] | returns `UserGuide` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| POST | `/user-guides/{userGuideId}/restart` | `userGuideRestart` (M) | Restart a user guide from step 0 while preserving completion history. [G] | returns `UserGuideProgress` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |
| POST | `/user-guides/{userGuideId}/start` | `userGuideStart` (M) | Start a user guide. [G] | returns `UserGuideProgress` [G]; Bruno marks it advanced: drives the in-app onboarding wizard [B] |

### User Guide: Fields of `UserGuideChapter` (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/user-guide-chapters/{userGuideChapterId}/guides` | `guides` on `UserGuideChapter` | List of guides in this chapter. [G] | returns `[UserGuide]` [G] |

### User Guide: Fields of `UserGuideGroup` (1)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/user-guide-groups/{userGuideGroupId}/chapters` | `chapters` on `UserGuideGroup` | List of chapters in this group. [G] | returns `[UserGuideChapter]` [G] |

### Forms (3)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| GET | `/generic-forms-list` | `genericFormsList` (Q) | Returns a list of available generic forms. [G] | returns `[GenericFormDescription]` [G]; Bruno marks it advanced: in-app survey and onboarding forms [B] |
| PUT | `/generic-forms/{name}` | `completeGenericForm` (M) | Complete generic form with the data provided in input. [G] | body `CompleteGenericFormRequest`; returns `Boolean` [G]; Bruno marks it advanced: in-app survey and onboarding forms [B] |
| GET | `/is-generic-form-completed` | `isGenericFormCompleted` (Q) | Returns true if the generic form is completed. [G] | query `name`; returns `Boolean` [G]; Bruno marks it advanced: in-app survey and onboarding forms [B] |

## 14. Untagged operations

Eight operations carry no tag in the reference: Bruno files all of them
under Danger Zone, and the tag resolver finds no resource folder for them
**[B][I]**. Each belongs with a family above:

| GraphQL field | Family | Why it is dangerous |
|---|---|---|
| `identityFederationUnlink` | Managed Users (2.6) | Removes a login method from the caller **[G]** |
| `openTofuMigrate` | OpenTofu Migration (7.4) | Migrates every stack the filter matches **[G]** |
| `openTofuMigrationQueueClean` | OpenTofu Migration (7.4) | Drops all pending queue entries **[G]** |
| `deleteOauthClientForSCIM` | SSO (2.7) | Breaks SCIM provisioning **[G]** |
| `resetOauthClientForSCIM` | SSO (2.7) | Invalidates the SCIM client credentials **[G]** |
| `userSecurityKeyDeleteAll` | Sessions and Security Keys (2.8) | Removes every security key of the caller **[G]** |
| `sessionDeleteAll` | Sessions and Security Keys (2.8) | Ends every session but the caller's **[G]** |
| `stackMigrateVendorAll` | Stacks (report 03) | Changes vendor configuration of every eligible stack **[G]** |

### (untagged) (8)

| Method | Path | GraphQL field | Purpose | Notes |
|---|---|---|---|---|
| DELETE | `/identity-federations/{provider}` | `identityFederationUnlink` (M) | Unlink an external provider from the current user's identity. [G] | returns `Identity` [G]; Bruno files it under Danger Zone [B] |
| POST | `/open-tofu-migration-queue/clean` | `openTofuMigrationQueueClean` (M) | Removes all pending (TO_MIGRATE / TO_ROLLBACK) entries from the migration queue. Already-processed entries (MIGRATED, ROLLEDBACK, FAILED) are not affected. [G] | returns `OpenTofuMigrationQueueCleanResult` [G]; Bruno files it under Danger Zone [B] |
| POST | `/open-tofu-migrations` | `openTofuMigrate` (M) | Enqueues OpenTofu vendor migration (or rollback) for every stack that matches the given search filter. The mutation is asynchronous. [G] | body `OpenTofuMigrateRequest`; returns `OpenTofuMigrateResult` [G]; Bruno files it under Danger Zone [B]; Enqueues migration or rollback for every matching stack [G] |
| DELETE | `/scim-settings` | `deleteOauthClientForSCIM` (M) | Delete the OAuth2 client for SCIM provisioning. [G] | returns `Boolean` [G]; Tier `SCIM` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/single-sign-on/scim); Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| POST | `/scim-settings/reset` | `resetOauthClientForSCIM` (M) | Reset the OAuth2 client credentials for SCIM provisioning. [G] | returns `ScimOauthClient` [G]; Tier `SCIM` [I]; Enterprise [[D]](https://docs.spacelift.io/integrations/single-sign-on/scim); Bruno files it under Danger Zone [B]; Bruno marks it advanced: account-level sso, saml, scim or oidc configuration [B] |
| DELETE | `/security-keys` | `userSecurityKeyDeleteAll` (M) | Delete all security keys for the current user. [G] | returns `[SecurityKey]` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: manages your own sessions and security keys [B] |
| DELETE | `/sessions` | `sessionDeleteAll` (M) | Invalidate all account sessions except for the session making the request. Returns the deleted sessions. [G] | returns `[Session]` [G]; Bruno files it under Danger Zone [B]; Bruno marks it advanced: manages your own sessions and security keys [B]; Keeps the calling session [G] |
| POST | `/stacks/migrate-vendor` | `stackMigrateVendorAll` (M) | Migrate stack vendor configuration for all stacks that need it. The operation has a time limit and may not migrate all stacks in a single call. [G] | body `StackMigrateVendorAllRequest`; returns `StackMigrateVendorAllResult` [G]; Bruno files it under Danger Zone [B]; Time-limited; run again until done [G] |

## What this means for infraweave

- The platform surface is larger than the deployment core: identity, SSO,
  SCIM, sessions, webhooks, audit and registries each need their own routes
  and permission rules, most of them outside the `Action` catalog.
- Several tags exist only for the web UI (search suggestions, UI state, user
  guide, forms, saved filters); the plan must decide which of them the
  infraweave UI needs, since they are part of the mirrored surface.
- Billing and tier operations describe a SaaS business model; a self-hosted
  infraweave still needs an answer for `tier` and `tierFeatures`, which
  clients read.
- Self-hosted-only operations (feature flags, debug info, queue stats) have
  SaaS behaviour defined (empty results), which is a compatibility contract.
- The Danger Zone operations need confirmation or guard rails in any client.

## Inferred and undocumented

- Scans, the Origin integration, blob storage and plugin templates have no
  docs page in the mirror *(unverified)*; their tables rest on the schema and
  Bruno only.
- `cliConfirmationToken` backing the spacectl browser login is inferred from
  its description **[I]**; report 06 checks spacectl's source.
- `vcsAgentPoolReset` invalidating old agent credentials is inferred by
  analogy with `workerPoolReset` **[I]** *(unverified)*.
- The audit trail having a single webhook is inferred from its singleton
  routes **[I]**.
- Which plan gates external integrations other than ServiceNow is not stated
  *(unverified)*.

## Versions at the research baseline

| Input | Version |
|---|---|
| Introspection | `b3255fe38f5e`, fetched 2026-10-28 from `demo.app.spacelift.io` |
| spacelift-api-bruno | `7cad4574954b` |
| terraform-provider-spacelift | v1.57.0 |
| Docs mirror | read 2026-10-28 |

## Sources

- [`api/reference/spacelift/spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml): routes, tags, `x-graphql`, `x-bruno`.
- [`api/reference/spacelift/introspection.json`](../../../api/reference/spacelift/introspection.json) **[G]**.
- [`api/reference/spacelift/bruno.json`](../../../api/reference/spacelift/bruno.json) **[B]**.
- [`api/reference/spacelift/coverage.md`](../../../api/reference/spacelift/coverage.md): exclusions.
- [03-deploy-core-api-inventory.md](03-deploy-core-api-inventory.md): conventions, RBAC catalog and tier table.
- <https://docs.spacelift.io/integrations/api>, <https://docs.spacelift.io/concepts/authorization/rbac-system>, <https://docs.spacelift.io/concepts/authorization/assigning-roles-users>, <https://docs.spacelift.io/concepts/authorization/assigning-roles-stacks> **[D]**
- <https://docs.spacelift.io/integrations/single-sign-on>, <https://docs.spacelift.io/integrations/single-sign-on/scim>, <https://docs.spacelift.io/product/security/mfa> **[D]**
- <https://docs.spacelift.io/integrations/source-control>, <https://docs.spacelift.io/integrations/source-control/github>, <https://docs.spacelift.io/integrations/source-control/gitlab>, <https://docs.spacelift.io/concepts/vcs-agent-pools>, <https://docs.spacelift.io/concepts/repos> **[D]**
- <https://docs.spacelift.io/integrations/cloud-providers>, <https://docs.spacelift.io/integrations/cloud-providers/oidc>, <https://docs.spacelift.io/concepts/configuration/context> **[D]**
- <https://docs.spacelift.io/integrations/webhooks>, <https://docs.spacelift.io/integrations/audit-trail>, <https://docs.spacelift.io/product/notifications>, <https://docs.spacelift.io/integrations/chatops/slack>, <https://docs.spacelift.io/integrations/external-integrations/servicenow>, <https://docs.spacelift.io/integrations/external-integrations/backstage> **[D]**
- <https://docs.spacelift.io/vendors/terraform/provider-registry>, <https://docs.spacelift.io/vendors/terraform/version-management>, <https://docs.spacelift.io/vendors/opentofu/migration> **[D]**
- <https://docs.spacelift.io/concepts/stack/drift-detection>, <https://docs.spacelift.io/vendors/ansible>, <https://docs.spacelift.io/concepts/resources> **[D]**
- <https://docs.spacelift.io/concepts/template>, <https://docs.spacelift.io/concepts/blueprint> **[D]**
- <https://docs.spacelift.io/concepts/intelligence/ai-integrations>, <https://docs.spacelift.io/concepts/intelligence/intent> **[D]**
- <https://docs.spacelift.io/integrations/plugins>, <https://docs.spacelift.io/concepts/run> **[D]**
