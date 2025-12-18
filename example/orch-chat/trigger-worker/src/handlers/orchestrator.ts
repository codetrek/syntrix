import { Request, Response } from 'express';
import { AzureOpenAI } from 'openai';
import { SyntrixClient } from '../syntrix-client';
import { WebhookPayload, AgentTask, SubAgent, SubAgentMessage } from '../types';
import { v4 as uuidv4 } from 'uuid';

const openai = new AzureOpenAI({
  endpoint: process.env.AZURE_OPENAI_ENDPOINT,
  apiKey: process.env.AZURE_OPENAI_API_KEY,
  apiVersion: '2024-05-01-preview',
});

const syntrix = new SyntrixClient(process.env.SYNTRIX_API_URL);

const ORCHESTRATOR_TOOLS = [
  {
    type: 'function' as const,
    function: {
      name: 'create_agent',
      description: 'Create a new sub-agent to handle a complex task.',
      parameters: {
        type: 'object',
        properties: {
          type: {
            type: 'string',
            enum: ['researcher', 'general'],
            description: 'The type of agent to create.',
          },
          instruction: {
            type: 'string',
            description: 'Detailed instruction for the agent.',
          },
        },
        required: ['type', 'instruction'],
      },
    },
  },
  {
    type: 'function' as const,
    function: {
      name: 'reactivate_agent',
      description: 'Reactivate an existing sub-agent with new instructions.',
      parameters: {
        type: 'object',
        properties: {
          subAgentId: {
            type: 'string',
            description: 'The ID of the sub-agent to reactivate.',
          },
          instruction: {
            type: 'string',
            description: 'The new instruction for the agent.',
          },
        },
        required: ['subAgentId', 'instruction'],
      },
    },
  },
];

export const orchestratorHandler = async (req: Request, res: Response) => {
  try {
    const payload = req.body as WebhookPayload;
    console.log(`Received Orchestrator trigger: ${payload.triggerId}`);

    // Parse Context
    // collection: users/demo-user/orch-chats/chat-1/messages
    const parts = payload.collection.split('/');
    const userId = parts[1];
    const chatId = parts[3];
    console.log(`[Orchestrator] ChatID: ${chatId}`);
    const chatPath = `users/${userId}/orch-chats/${chatId}`;

    // Fetch History
    const history = await syntrix.fetchHistory(chatPath);

    // Check if Trigger Message is Last
    if (history.length > 0) {
        const lastMsg = history[history.length - 1];
        const triggerMsgId = (payload.after as any).id;
        if (lastMsg.id !== triggerMsgId) {
             console.log(`[Orchestrator] Trigger message ${triggerMsgId} is not the last message (Last: ${lastMsg.id}). Skipping.`);
             res.status(200).send('Skipped (Not Last Message)');
             return;
        }
    }

    // Fetch Recent Tasks (Agents) to provide context to Orchestrator
    // We query 'tasks' instead of 'sub-agents' because tasks contain the semantic info (name, instruction).
    const recentTasks = await syntrix.query<AgentTask>({
        collection: `users/${userId}/orch-chats/${chatId}/tasks`,
        orderBy: [{ field: 'updatedAt', direction: 'desc' }],
        limit: 5
    });

    // Fetch Waiting Tasks (Agents needing input)
    const waitingTasks = recentTasks.filter(t => t.status === 'waiting');

    // For each waiting task, get the last question
    const waitingAgentsInfo = await Promise.all(waitingTasks.map(async (task) => {
        if (!task.subAgentId) return null;
        const msgs = await syntrix.query<SubAgentMessage>({
            collection: `users/${userId}/orch-chats/${chatId}/sub-agents/${task.subAgentId}/messages`,
            orderBy: [{ field: 'createdAt', direction: 'desc' }],
            limit: 1
        });
        const question = (msgs.length > 0 && msgs[0].role === 'assistant') ? msgs[0].content : "Unknown question";
        return {
            subAgentId: task.subAgentId,
            name: task.name,
            question
        };
    }));
    const validWaitingAgents = waitingAgentsInfo.filter(a => a !== null);

    // --- Context Injection for Caching Optimization ---
    // 1. Static System Prompt (Cacheable Prefix)
    const staticSystemPrompt = `You are the Orchestrator. Your job is to manage the conversation.
    You can answer directly if the user's request is simple.
    If the request is complex (e.g. "research X", "write a report"), delegate it to a sub-agent.

    Routing Rules:
    1. If the user's message answers a waiting agent's question, use 'reply_to_agent'.
    2. If the user is following up on a previous agent's work (not waiting), use 'reactivate_agent'.
    3. If it's a new complex task, use 'create_agent'.
    4. Otherwise, reply directly.`;

    // 2. Dynamic Context (Injected at the end)
    const dynamicContext = `Current System State:

    Existing Sub-Agents (Tasks):
    ${recentTasks.filter(t => t.subAgentId).map(t => `- Name: ${t.name}, AgentID: ${t.subAgentId}, Status: ${t.status}, Instruction: ${t.instruction}`).join('\n')}

    WAITING AGENTS (These agents are waiting for user input):
    ${validWaitingAgents.map(a => `- Agent: ${a!.name} (ID: ${a!.subAgentId}) is asking: "${a!.question}"`).join('\n')}

    (Use this state to decide on the routing for the latest user message)`;

    // 3. Construct Messages (Split History)
    // [Static System] + [History (0...N-1)] + [Dynamic Context] + [Latest User Message (N)]
    const previousHistory = history.slice(0, -1);
    const lastMsg = history[history.length - 1];

    const messages = [
        { role: 'system', content: staticSystemPrompt },
        ...previousHistory.map(m => ({
          role: m.role as any,
          content: m.content,
          tool_calls: m.tool_calls,
          tool_call_id: m.tool_call_id
        })),
        { role: 'system', content: dynamicContext },
        {
          role: lastMsg.role as any,
          content: lastMsg.content,
          tool_calls: lastMsg.tool_calls,
          tool_call_id: lastMsg.tool_call_id
        }
    ];

    const completion = await openai.chat.completions.create({
      model: process.env.AZURE_OPENAI_MODEL || 'gpt-4o',
      messages: messages as any,
      tools: [
          ...ORCHESTRATOR_TOOLS,
          {
            type: 'function' as const,
            function: {
              name: 'reply_to_agent',
              description: 'Provide input to a waiting sub-agent.',
              parameters: {
                type: 'object',
                properties: {
                  subAgentId: { type: 'string' },
                  content: { type: 'string', description: 'The answer/input for the agent.' }
                },
                required: ['subAgentId', 'content']
              }
            }
          }
      ],
    });

    const message = completion.choices[0].message;

    if (message.tool_calls && message.tool_calls.length > 0) {
        const toolCall = message.tool_calls[0];
        const args = JSON.parse(toolCall.function.arguments);

        if (toolCall.function.name === 'create_agent') {
            // Create Task
            const taskId = uuidv4();
            const task: AgentTask = {
                id: taskId,
                userId,
                chatId,
                type: 'agent',
                name: `agent:${args.type}`,
                instruction: args.instruction,
                triggerMessageId: payload.docKey, // The user message ID
                status: 'pending',
                createdAt: Date.now(),
                updatedAt: Date.now()
            };
            await syntrix.createDocument(`users/${userId}/orch-chats/${chatId}/tasks`, task);
        } else if (toolCall.function.name === 'reactivate_agent') {
            // Reactivate Agent
            const subAgentId = args.subAgentId;
            // 1. Update SubAgent status
            await syntrix.updateDocument(`users/${userId}/orch-chats/${chatId}/sub-agents/${subAgentId}`, {
                status: 'active',
                updatedAt: Date.now()
            });

            // 2. Inject User Message into SubAgent
            await syntrix.createDocument(`users/${userId}/orch-chats/${chatId}/sub-agents/${subAgentId}/messages`, {
                id: uuidv4(),
                userId,
                subAgentId,
                role: 'user',
                content: args.instruction,
                createdAt: Date.now()
            });

            // 3. Update parent task status
            const tasks = await syntrix.query<AgentTask>({
                collection: `users/${userId}/orch-chats/${chatId}/tasks`,
                filters: [{ field: 'subAgentId', op: '==', value: subAgentId }]
            });
            if (tasks.length > 0) {
                await syntrix.updateDocument(`users/${userId}/orch-chats/${chatId}/tasks/${tasks[0].id}`, {
                    status: 'running',
                    updatedAt: Date.now()
                });
            }
        } else if (toolCall.function.name === 'reply_to_agent') {
            // Reply to Agent
            const subAgentId = args.subAgentId;
            const content = args.content;

            // 1. Update SubAgent status
            await syntrix.updateDocument(`users/${userId}/orch-chats/${chatId}/sub-agents/${subAgentId}`, {
                status: 'active',
                updatedAt: Date.now()
            });

            // 2. Inject User Message (from Orchestrator) into SubAgent
            await syntrix.createDocument(`users/${userId}/orch-chats/${chatId}/sub-agents/${subAgentId}/messages`, {
                id: uuidv4(),
                userId,
                subAgentId,
                role: 'user',
                content: content,
                createdAt: Date.now()
            });

            // 3. Update parent task status
            const tasks = await syntrix.query<AgentTask>({
                collection: `users/${userId}/orch-chats/${chatId}/tasks`,
                filters: [{ field: 'subAgentId', op: '==', value: subAgentId }]
            });
            if (tasks.length > 0) {
                await syntrix.updateDocument(`users/${userId}/orch-chats/${chatId}/tasks/${tasks[0].id}`, {
                    status: 'running',
                    updatedAt: Date.now()
                });
            }

            // 4. Notify User (Optional but good for UX)
            await syntrix.postMessage(chatPath, `(Forwarded to Agent: "${content}")`);
        }
    } else if (message.content) {
        // Direct Reply
        await syntrix.postMessage(chatPath, message.content);
    }

    res.status(200).send('OK');
  } catch (error) {
    console.error('Orchestrator Handler Error:', error);
    res.status(500).send('Internal Server Error');
  }
};
