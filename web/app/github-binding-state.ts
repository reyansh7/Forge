/** Words for the application GitHub card.

Push-to-deploy is on only when this application has a saved binding
and the API registered a webhook. A linked GitHub account, or a Git
URL on the project, is not that.
 */

export type PushDeployStatus = {
  enabled: boolean;
  label: string;
  detail: string;
};

export function pushDeployStatus(input: {
  oauthConnected: boolean;
  bindingConnected: boolean;
  webhook?: boolean;
  warning?: string;
}): PushDeployStatus {
  if (!input.bindingConnected) {
    if (input.oauthConnected) {
      return {
        enabled: false,
        label: "Push-to-deploy is off",
        detail: "GitHub is linked to this account. Save a repository and branch before a push can deploy.",
      };
    }
    return {
      enabled: false,
      label: "Push-to-deploy is off",
      detail: "Link GitHub, then save a repository and branch. Signing in alone does not deploy pushes.",
    };
  }
  if (input.webhook) {
    return {
      enabled: true,
      label: "Push-to-deploy is on",
      detail: "A push to the saved branch queues one deployment of that commit.",
    };
  }
  return {
    enabled: false,
    label: "Push-to-deploy is off",
    detail:
      input.warning?.trim() ||
      "The repository is saved. Forge did not register a webhook because this API has no public address.",
  };
}
