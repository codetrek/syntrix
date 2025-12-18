import { Request, Response } from 'express';
import { SyntrixClient } from '../syntrix-client';
import { WebhookPayload, AgentTask, SubAgent } from '../types';
import { v4 as uuidv4 } from 'uuid';

const syntrix = new SyntrixClient(process.env.SYNTRIX_API_URL);

export const agentInitHandler = async (req: Request, res: Response) => {
  try {
    const payload = req.body as WebhookPayload;
    console.log(`Received Agent Init trigger: ${payload.triggerId}`);

    const task = payload.after as AgentTask;
    if (!task || task.type !== 'agent') {
        res.status(200).send('Ignored');
        return;
    }

    // collection: users/demo-user/orch-chats/chat-1/tasks
    const parts = payload.collection.split('/');
    const userId = parts[1];
    const chatId = parts[3];

    console.log(`[AgentInit] ChatID: ${chatId}, TaskID: ${task.id}`);

    const subAgentId = uuidv4();
    const subAgent: SubAgent = {
        id: subAgentId,
        userId,
        chatId,
        taskId: task.id,
        role: task.name.split(':')[1] as any, // agent:researcher -> researcher
        status: 'active',
        createdAt: Date.now(),
        updatedAt: Date.now()
    };

    // 1. Create SubAgent
    await syntrix.createDocument(`users/${userId}/orch-chats/${chatId}/sub-agents`, subAgent);

    // 2. Update Task
    await syntrix.updateDocument(`users/${userId}/orch-chats/${chatId}/tasks/${task.id}`, {
        subAgentId: subAgentId,
        status: 'running',
        updatedAt: Date.now()
    });

    // 3. Bootstrap Messages
    const systemPrompt = `You are a ${subAgent.role}.
    Instruction: ${task.instruction}

    CRITICAL: You MUST end every turn with either a Tool Call or a Final Answer. Do not just chat.
    If you need clarification, ask the user.`;

    // System Message
    await syntrix.createDocument(`users/${userId}/orch-chats/${chatId}/sub-agents/${subAgentId}/messages`, {
        id: uuidv4(),
        userId,
        subAgentId,
        role: 'system',
        content: systemPrompt,
        createdAt: Date.now()
    });

    // User Message (Instruction)
    await syntrix.createDocument(`users/${userId}/orch-chats/${chatId}/sub-agents/${subAgentId}/messages`, {
        id: uuidv4(),
        userId,
        subAgentId,
        role: 'user',
        content: task.instruction,
        createdAt: Date.now() + 1
    });

    res.status(200).send('OK');
  } catch (error) {
    console.error('Agent Init Handler Error:', error);
    res.status(500).send('Internal Server Error');
  }
};
