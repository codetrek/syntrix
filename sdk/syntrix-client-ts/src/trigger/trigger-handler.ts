import { TriggerClient } from './trigger-client';

export interface WebhookPayload {
  triggerId: string;
  collection: string;
  documentId?: string;
  preIssuedToken?: string;
  before?: unknown;
  after?: unknown;
}

export class TriggerHandler {
  public readonly syntrix: TriggerClient;

  constructor(payload: WebhookPayload, baseUrl: string) {
    if (!payload.preIssuedToken) {
      throw new Error('Missing preIssuedToken in webhook payload');
    }
    this.syntrix = new TriggerClient(baseUrl, payload.preIssuedToken);
  }
}
