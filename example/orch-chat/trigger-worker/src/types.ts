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

export interface AgentTask {
  id: string;
  userId: string;
  chatId: string;
  type: 'agent' | 'tool';
  name: string;
  instruction: string;
  subAgentId?: string;
  triggerMessageId: string;
  status: 'pending' | 'running' | 'success' | 'failed' | 'waiting';
  result?: string;
  error?: string;
  createdAt: number;
  updatedAt: number;
}

export interface SubAgent {
  id: string;
  userId: string;
  chatId: string;
  taskId: string;
  role: 'general' | 'researcher';
  status: 'active' | 'completed' | 'failed' | 'waiting_for_user';
  summary?: string;
  createdAt: number;
  updatedAt: number;
}

export interface SubAgentMessage {
  id: string;
  userId: string;
  subAgentId: string;
  role: 'system' | 'user' | 'assistant' | 'tool';
  content: string;
  toolCallId?: string;
  toolCalls?: any[];
  createdAt: number;
}

export interface SubAgentToolCall {
  id: string;
  userId: string;
  subAgentId: string;
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
