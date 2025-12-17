import axios, { AxiosInstance } from 'axios';
import { Message, SyntrixQuery, ToolCall } from './types';
import { v4 as uuidv4 } from 'uuid';

export class SyntrixClient {
  private client: AxiosInstance;

  constructor(baseURL: string = 'http://localhost:8080') {
    this.client = axios.create({
      baseURL,
      headers: {
        'Content-Type': 'application/json',
      },
    });
  }

  async query<T>(query: SyntrixQuery): Promise<T[]> {
    try {
      const response = await this.client.post('/v1/query', query);
      // Syntrix Query API returns { documents: [...] } or just array?
      // Based on internal/query/engine.go, it returns []*storage.Document
      // But internal/api/server.go handleQuery likely wraps it.
      // Let's assume it returns the list of documents directly or wrapped.
      // Checking internal/api/server.go would be good, but let's assume standard JSON response.
      // If it returns raw documents, they have { id, collection, data, ... }
      // We usually want just the data with ID injected.

      // Wait, let's check internal/api/server.go handleQuery implementation if possible.
      // Assuming it returns a list of documents.
      const docs = response.data;
      if (Array.isArray(docs)) {
          return docs.map((d: any) => ({ ...d.data, id: d.id }));
      }
      return [];
    } catch (error) {
      console.error('Query failed:', error);
      return [];
    }
  }

  async createDocument(path: string, data: any) {
    try {
      await this.client.post(`/v1/${path}`, data);
    } catch (error) {
      console.error(`Failed to create document at ${path}:`, error);
      throw error;
    }
  }

  async updateDocument(path: string, data: any) {
    try {
      await this.client.patch(`/v1/${path}`, data);
    } catch (error) {
      console.error(`Failed to update document at ${path}:`, error);
      throw error;
    }
  }

  async fetchHistory(chatPath: string): Promise<Message[]> {
    // 1. Fetch Messages
    const messages = await this.query<Message>({
      collection: `${chatPath}/messages`,
      orderBy: [{ field: 'createdAt', direction: 'asc' }],
    });

    // 2. Fetch Tool Calls
    const toolCalls = await this.query<ToolCall>({
      collection: `${chatPath}/toolcall`,
      orderBy: [{ field: 'createdAt', direction: 'asc' }],
    });

    // 3. Merge and Format
    const history: Message[] = [];

    // We need to interleave them based on createdAt
    const allEvents = [
      ...messages.map(m => ({ type: 'message', data: m })),
      ...toolCalls.map(t => ({ type: 'tool', data: t }))
    ].sort((a, b) => a.data.createdAt - b.data.createdAt);

    for (const event of allEvents) {
      if (event.type === 'message') {
        const m = event.data as Message;
        history.push({
          id: m.id,
          role: m.role,
          content: m.content,
          createdAt: m.createdAt
        });
      } else {
        const t = event.data as ToolCall;
        // Tool Call (Request) -> Assistant Message with tool_calls
        history.push({
          id: t.id,
          role: 'assistant',
          content: null,
          tool_calls: [{
            id: t.id,
            type: 'function',
            function: {
              name: t.toolName,
              arguments: JSON.stringify(t.args)
            }
          }],
          createdAt: t.createdAt
        });

        // Tool Result -> Tool Message
        if (t.status === 'success' && t.result) {
          history.push({
            id: `${t.id}-result`,
            role: 'tool',
            tool_call_id: t.id,
            content: t.result,
            createdAt: t.createdAt + 1 // Ensure it comes after the call
          });
        }
      }
    }

    return history;
  }

  async postMessage(chatPath: string, content: string) {
    const id = uuidv4();
    await this.createDocument(`${chatPath}/messages/${id}`, {
      id,
      role: 'assistant',
      content,
      createdAt: Date.now()
    });
  }

  async postToolCall(chatPath: string, toolName: string, args: any, toolCallId: string) {
    // Use the ID from OpenAI if provided, otherwise generate one
    const id = toolCallId || uuidv4();
    await this.createDocument(`${chatPath}/toolcall/${id}`, {
      id,
      toolName,
      args,
      status: 'pending',
      createdAt: Date.now()
    });
  }

  async updateToolCall(chatPath: string, callId: string, result: string) {
    await this.updateDocument(`${chatPath}/toolcall/${callId}`, {
      status: 'success',
      result
    });
  }
}
