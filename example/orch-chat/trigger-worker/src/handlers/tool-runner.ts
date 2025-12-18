import { Request, Response } from 'express';
import { SyntrixClient } from '../syntrix-client';
import { TavilyClient } from '../tools/tavily';
import { WebhookPayload } from '../types';
import { v4 as uuidv4 } from 'uuid';

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
    // collection: users/demo-user/orch-chats/chat-1/toolcall
    // OR: users/demo-user/orch-chats/chat-1/sub-agents/agent-1/tool-calls
    const parts = payload.collection.split('/');
    const isSubAgent = parts.includes('sub-agents');

    let result = '';

    // Execute Tool
    if (toolName === 'tavily_search') {
      console.log(`Executing Tavily Search: ${args.query}`);
      result = await tavily.search(args.query, {
        searchDepth: args.searchDepth,
        maxResults: args.maxResults,
        includeDomains: args.includeDomains,
        excludeDomains: args.excludeDomains,
        includeRawContent: args.includeRawContent
      });
    } else {
      result = `Error: Unknown tool ${toolName}`;
    }

    if (isSubAgent) {
        const userId = parts[1];
        const chatId = parts[3];
        const subAgentId = parts[5];
        console.log(`[ToolRunner] ChatID: ${chatId}, AgentID: ${subAgentId}, ToolCallID: ${callId}`);
        const subAgentPath = `users/${userId}/orch-chats/${chatId}/sub-agents/${subAgentId}`;

        // 1. Update Tool Call Document
        await syntrix.updateDocument(`${subAgentPath}/tool-calls/${callId}`, {
            status: 'success',
            result,
            updatedAt: Date.now()
        });

        // 2. Insert Tool Message (Triggers Agent Loop)
        await syntrix.createDocument(`${subAgentPath}/messages`, {
            id: uuidv4(),
            userId,
            subAgentId,
            role: 'tool',
            content: result,
            toolCallId: callId,
            createdAt: Date.now()
        });
    } else {
        // Legacy / Main Chat Tool Call
        const userId = parts[1];
        const chatId = parts[3];
        console.log(`[ToolRunner] ChatID: ${chatId}, ToolCallID: ${callId}`);
        const chatPath = `users/${userId}/orch-chats/${chatId}`;

        // Update Tool Call Document
        await syntrix.updateToolCall(chatPath, callId, result);
    }

    res.status(200).send('OK');
  } catch (error) {
    console.error('Tool Runner Error:', error);
    res.status(500).send('Internal Server Error');
  }
};
