import { Request, Response } from 'express';
import { SyntrixClient } from '../syntrix-client';
import { WebhookPayload, AgentTask } from '../types';

const syntrix = new SyntrixClient(process.env.SYNTRIX_API_URL);

export const orchestratorFinalizeHandler = async (req: Request, res: Response) => {
  try {
    const payload = req.body as WebhookPayload;
    console.log(`Received Orchestrator Finalize trigger: ${payload.triggerId}`);

    const task = payload.after as AgentTask;
    if (!task || task.status !== 'success' || !task.result) {
        res.status(200).send('Ignored');
        return;
    }

    // collection: users/demo-user/orch-chats/chat-1/tasks
    const parts = payload.collection.split('/');
    const userId = parts[1];
    const chatId = parts[3];
    console.log(`[OrchestratorFinalize] ChatID: ${chatId}, TaskID: ${task.id}`);
    const chatPath = `users/${userId}/orch-chats/${chatId}`;

    const content = `**Agent ${task.name} Completed:**\n\n${task.result}`;

    // Idempotency Check: Prevent duplicate completion messages
    const history = await syntrix.fetchHistory(chatPath);
    if (history.length > 0) {
        const lastMsg = history[history.length - 1];
        if (lastMsg.role === 'assistant' && lastMsg.content && lastMsg.content.startsWith(`**Agent ${task.name} Completed:**`)) {
            console.log(`[OrchestratorFinalize] Duplicate completion message detected. Skipping.`);
            res.status(200).send('Skipped (Duplicate)');
            return;
        }
    }

    await syntrix.postMessage(chatPath, content);

    res.status(200).send('OK');
  } catch (error) {
    console.error('Orchestrator Finalize Handler Error:', error);
    res.status(500).send('Internal Server Error');
  }
};
