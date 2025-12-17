import { Request, Response } from 'express';
import { SyntrixClient } from '../syntrix-client';
import { TavilyClient } from '../tools/tavily';
import { WebhookPayload } from '../types';

const syntrix = new SyntrixClient(process.env.SYNTRIX_API_URL);
const tavily = new TavilyClient(process.env.TAVILY_API_KEY || '');

export const toolRunnerHandler = async (req: Request, res: Response) => {
  try {
    const payload = req.body as WebhookPayload;
    console.log(`Received Tool trigger: ${payload.triggerId}`);

    const doc = payload.after;
    if (!doc || doc.status !== 'pending') {
      // Ignore if not pending or no data
      res.status(200).send('Ignored');
      return;
    }

    const toolName = doc.toolName;
    const args = doc.args;
    const callId = doc.id; // Assuming ID is in the document data

    // Parse chat path from collection
    // collection: users/demo-user/chats/chat-1/toolcall
    const parts = payload.collection.split('/');
    const userId = parts[1];
    const chatId = parts[3];
    const chatPath = `users/${userId}/chats/${chatId}`;

    let result = '';

    // Execute Tool
    if (toolName === 'tavily_search') {
      console.log(`Executing Tavily Search: ${args.query}`);
      result = await tavily.search(args.query);
    } else {
      result = `Error: Unknown tool ${toolName}`;
    }

    // Update Tool Call Document
    await syntrix.updateToolCall(chatPath, callId, result);

    res.status(200).send('OK');
  } catch (error) {
    console.error('Tool Runner Error:', error);
    res.status(500).send('Internal Server Error');
  }
};
