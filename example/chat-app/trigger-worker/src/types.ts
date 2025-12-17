export interface WebhookPayload {
  triggerId: string;
  event: 'create' | 'update' | 'delete';
  collection: string;
  docKey: string;
  after?: any;
  before?: any;
  ts: number;
}

export interface Message {
  id: string;
  role: 'user' | 'assistant' | 'system' | 'tool';
  content: string | null;
  tool_calls?: any[];
  tool_call_id?: string;
  createdAt: number;
}

export interface ToolCall {
  id: string;
  toolName: string;
  args: any;
  status: 'pending' | 'success' | 'failed';
  result?: string;
  error?: string;
  createdAt: number;
}

export interface SyntrixQuery {
  collection: string;
  filters?: Array<{
    field: string;
    op: string;
    value: any;
  }>;
  orderBy?: Array<{
    field: string;
    direction: 'asc' | 'desc';
  }>;
  limit?: number;
}
