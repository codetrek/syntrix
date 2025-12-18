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
      // Syntrix Query API returns flattened documents (map[string]interface{})
      // So response.data is an array of objects like { id: "...", role: "...", ... }
      const docs = response.data;
      if (Array.isArray(docs)) {
          return docs as T[];
      }
      return [];
    } catch (error) {
      console.error('Query failed:', error);
      return [];
    }
  }

  async getDocument<T>(path: string): Promise<T | null> {
    try {
      const response = await this.client.get(`/v1/${path}`);
      return response.data as T;
    } catch (error) {
      // console.error(`Failed to get document at ${path}:`, error);
      return null;
    }
  }

  async createDocument(path: string, data: any) {
    try {
      await this.client.post(`/v1/${path}`, data);
    } catch (error) {
      if (axios.isAxiosError(error) && error.response?.status === 409) {
        console.error(`Document at ${path} already exists.`);
        throw new Error('Document already exists');
      }
      console.error(`Failed to create document at ${path}:`, error);
      throw error;
    }
  }

  async updateDocument(path: string, data: any) {
    try {
      await this.client.patch(`/v1/${path}`, data);
    } catch (error) {
      if (axios.isAxiosError(error) && error.response?.status === 404) {
        console.error(`Document at ${path} not found for update.`);
        throw new Error('Document not found');
      }
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
    // POST to the collection, not the document path
    await this.createDocument(`${chatPath}/messages`, {
      id,
      role: 'assistant',
      content,
      createdAt: Date.now()
    });
  }

  async postToolCall(chatPath: string, toolName: string, args: any, toolCallId: string) {
    // Use the ID from OpenAI if provided, otherwise generate one
    const id = toolCallId || uuidv4();
    // POST to the collection
    await this.createDocument(`${chatPath}/toolcall`, {
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
