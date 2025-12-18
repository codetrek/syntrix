import { Request, Response } from 'express';
import { AzureOpenAI } from 'openai';
import { SyntrixClient } from '../syntrix-client';
import { WebhookPayload, AgentTask, SubAgentMessage } from '../types';

const openai = new AzureOpenAI({
  endpoint: process.env.AZURE_OPENAI_ENDPOINT,
  apiKey: process.env.AZURE_OPENAI_API_KEY,
  apiVersion: '2024-05-01-preview',
});

const syntrix = new SyntrixClient(process.env.SYNTRIX_API_URL);

export const orchestratorNotifyHandler = async (req: Request, res: Response) => {
  try {
    const payload = req.body as WebhookPayload;
    console.log(`Received Orchestrator Notify trigger: ${payload.triggerId}`);

    const task = payload.after as AgentTask;
    if (!task || task.status !== 'waiting' || !task.subAgentId) {
        res.status(200).send('Ignored');
        return;
    }

    // collection: users/demo-user/orch-chats/chat-1/tasks
    const parts = payload.collection.split('/');
    const userId = parts[1];
    const chatId = parts[3];
    console.log(`[OrchestratorNotify] ChatID: ${chatId}, TaskID: ${task.id}`);
    const chatPath = `users/${userId}/orch-chats/${chatId}`;

    // Fetch latest message from sub-agent
    const messages = await syntrix.query<SubAgentMessage>({
        collection: `users/${userId}/orch-chats/${chatId}/sub-agents/${task.subAgentId}/messages`,
        orderBy: [{ field: 'createdAt', direction: 'desc' }],
        limit: 1
    });

    let question = "The agent needs more information.";
    if (messages.length > 0 && messages[0].role === 'assistant') {
        question = messages[0].content;
    }

    // --- Proactive Orchestration ---
    // Check if we can answer this question automatically using chat history.

    const history = await syntrix.fetchHistory(chatPath);
    const systemPrompt = `You are the Orchestrator.
    A sub-agent is asking a question: "${question}".
    Check the conversation history below.
    If the answer is present and unambiguous, use the 'reply_to_agent' tool to answer it.
    If the answer is NOT present, use the 'ask_user' tool to notify the user.
    `;

    const completion = await openai.chat.completions.create({
        model: process.env.AZURE_OPENAI_MODEL || 'gpt-4o',
        messages: [
            { role: 'system', content: systemPrompt },
            ...history.map(m => ({
                role: m.role as any,
                content: m.content
            }))
        ],
        tools: [
            {
                type: 'function',
                function: {
                    name: 'reply_to_agent',
                    description: 'Answer the agent directly.',
                    parameters: {
                        type: 'object',
                        properties: {
                            answer: { type: 'string' }
                        },
                        required: ['answer']
                    }
                }
            },
            {
                type: 'function',
                function: {
                    name: 'ask_user',
                    description: 'Ask the user for input.',
                    parameters: {
                        type: 'object',
                        properties: {},
                    }
                }
            }
        ]
    });

    const message = completion.choices[0].message;
    if (message.tool_calls && message.tool_calls.length > 0) {
        const toolCall = message.tool_calls[0];
        const args = JSON.parse(toolCall.function.arguments);

        if (toolCall.function.name === 'reply_to_agent') {
            const answer = args.answer;
            console.log(`[OrchestratorNotify] Auto-answering agent: ${answer}`);

            // 1. Update SubAgent status
            await syntrix.updateDocument(`users/${userId}/orch-chats/${chatId}/sub-agents/${task.subAgentId}`, {
                status: 'active',
                updatedAt: Date.now()
            });

            // 2. Inject User Message (Auto-Answer)
            const { v4: uuidv4 } = await import('uuid');
            await syntrix.createDocument(`users/${userId}/orch-chats/${chatId}/sub-agents/${task.subAgentId}/messages`, {
                id: uuidv4(),
                userId,
                subAgentId: task.subAgentId,
                role: 'user',
                content: answer,
                createdAt: Date.now()
            });

            // 3. Update Task
            await syntrix.updateDocument(`users/${userId}/orch-chats/${chatId}/tasks/${task.id}`, {
                status: 'running',
                updatedAt: Date.now()
            });

            // 4. Log to Main Chat (Optional)
            await syntrix.postMessage(chatPath, `(Auto-replied to Agent: "${answer}")`);

        } else {
            // ask_user
            const content = `**Agent ${task.name} Needs Input:**\n\n${question}`;
            await syntrix.postMessage(chatPath, content);
        }
    } else {
        // Fallback
        const content = `**Agent ${task.name} Needs Input:**\n\n${question}`;
        await syntrix.postMessage(chatPath, content);
    }

    res.status(200).send('OK');
  } catch (error) {
    console.error('Orchestrator Notify Handler Error:', error);
    res.status(500).send('Internal Server Error');
  }
};
