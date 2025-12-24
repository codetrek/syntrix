import axios, { AxiosInstance } from 'axios';
import { Message, SyntrixQuery, ToolCall } from './types';
import { generateShortId } from './utils';

export class SyntrixClient {
  private client: AxiosInstance;

  constructor(baseURL: string, token: string) {
    if (!token) {
        throw new Error("SyntrixClient requires a pre-issued token");
    }
    this.client = axios.create({
      baseURL,
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
    });
  }

  async query<T>(query: SyntrixQuery): Promise<T[]> {
    try {
      const response = await this.client.post('/api/v1/trigger/query', query);
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
      const response = await this.client.post('/api/v1/trigger/get', { paths: [path] });
      if (response.data.documents && response.data.documents.length > 0) {
          return response.data.documents[0] as T;
      }
      return null;
    } catch (error) {
      // console.error(`Failed to get document at ${path}:`, error);
      return null;
    }
  }

  async createDocument(collectionPath: string, data: any) {
    try {
      let docPath = collectionPath;
      if (data.id) {
          const cleanPath = collectionPath.endsWith('/') ? collectionPath.slice(0, -1) : collectionPath;
          docPath = `${cleanPath}/${data.id}`;
      } else {
          throw new Error("Document ID required for trigger write");
      }

      await this.client.post('/api/v1/trigger/write', {
        writes: [{
          type: 'create',
          path: docPath,
          data: data
        }]
      });
    } catch (error) {
      console.error(`Failed to create document at ${collectionPath}:`, error);
      throw error;
    }
  }

  async updateDocument(path: string, data: any) {
    try {
      await this.client.post('/api/v1/trigger/write', {
        writes: [{
          type: 'update',
          path: path,
          data: data
        }]
      });
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
    const id = generateShortId();
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
    const id = toolCallId || generateShortId();
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
