import { Request, Response } from 'express';
import { AzureOpenAI } from 'openai';
import { SyntrixClient } from '../syntrix-client';
import { WebhookPayload } from '../types';

const openai = new AzureOpenAI({
  endpoint: process.env.AZURE_OPENAI_ENDPOINT,
  apiKey: process.env.AZURE_OPENAI_API_KEY,
  apiVersion: '2024-05-01-preview',
});

const syntrix = new SyntrixClient(process.env.SYNTRIX_API_URL);

const TOOLS = [
  {
    type: 'function' as const,
    function: {
      name: 'tavily_search',
      description: 'Search the web for current information.',
      parameters: {
        type: 'object',
        properties: {
          query: {
            type: 'string',
            description: 'The search query.',
          },
          searchDepth: {
            type: 'string',
            enum: ['basic', 'advanced'],
            description: 'Search depth: "basic" for faster results, "advanced" for more comprehensive results. Default is "basic".',
          },
          maxResults: {
            type: 'number',
            description: 'Maximum number of results to return. Default is 5.',
          },
          includeDomains: {
            type: 'array',
            items: { type: 'string' },
            description: 'List of domains to include in search.',
          },
          excludeDomains: {
            type: 'array',
            items: { type: 'string' },
            description: 'List of domains to exclude from search.',
          },
        },
        required: ['query'],
      },
    },
  },
];

export const llmHandler = async (req: Request, res: Response) => {
  try {
    const payload = req.body as WebhookPayload;
    console.log(`Received LLM trigger: ${payload.triggerId}`);

    // 1. Parse Context
    // collection: users/demo-user/orch-chats/chat-1/messages
    // or users/demo-user/orch-chats/chat-1/toolcall
    const parts = payload.collection.split('/');
    // parts: ["users", "demo-user", "chats", "chat-1", "messages"]
    if (parts.length < 5) {
      console.error('Invalid collection path:', payload.collection);
      res.status(400).send('Invalid path');
      return;
    }

    const userId = parts[1];
    const chatId = parts[3];
    const chatPath = `users/${userId}/orch-chats/${chatId}`;

    // 2. Reconstruct History
    const history = await syntrix.fetchHistory(chatPath);

    // 3. Safety Check (Max Turns)
    // Count how many tool calls are in the history
    const toolCallCount = history.filter(m => m.role === 'assistant' && m.tool_calls).length;
    if (toolCallCount > 5) {
      console.warn('Max tool turns reached. Forcing reply.');
      // Append system message to force stop
      history.push({
        id: 'system-stop',
        role: 'system',
        content: 'You have reached the maximum number of tool calls. Please provide a final answer based on the information you have.',
        createdAt: Date.now()
      });
    }

    // 4. Call OpenAI
    const completion = await openai.chat.completions.create({
      model: process.env.AZURE_OPENAI_MODEL || 'gpt-4o',
      messages: [
        { role: 'system', content: 'You are a helpful assistant. You have access to tools. Use them when needed.' },
        ...history.map(m => ({
          role: m.role as any,
          content: m.content,
          tool_calls: m.tool_calls,
          tool_call_id: m.tool_call_id
        }))
      ],
      tools: toolCallCount > 5 ? undefined : TOOLS, // Disable tools if limit reached
    });

    const choice = completion.choices[0];
    const message = choice.message;

    // 5. Handle Response
    if (message.tool_calls && message.tool_calls.length > 0) {
      // Handle Tool Calls
      for (const toolCall of message.tool_calls) {
        await syntrix.postToolCall(
          chatPath,
          toolCall.function.name,
          JSON.parse(toolCall.function.arguments),
          toolCall.id
        );
      }
    } else if (message.content) {
      // Handle Text Reply
      await syntrix.postMessage(chatPath, message.content);
    }

    res.status(200).send('OK');
  } catch (error) {
    console.error('LLM Handler Error:', error);
    res.status(500).send('Internal Server Error');
  }
};
