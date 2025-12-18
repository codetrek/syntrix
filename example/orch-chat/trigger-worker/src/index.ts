import 'dotenv/config';
import express from 'express';
import { llmHandler } from './handlers/llm';
import { toolRunnerHandler } from './handlers/tool-runner';
import { generateTitleHandler } from './handlers/generate-title';
import { orchestratorHandler } from './handlers/orchestrator';
import { agentInitHandler } from './handlers/agent-init';
import { agentLoopHandler } from './handlers/agent-loop';
import { orchestratorFinalizeHandler } from './handlers/orchestrator-finalize';
import { orchestratorNotifyHandler } from './handlers/orchestrator-notify';

const app = express();
const port = process.env.PORT || 3000;

app.use(express.json());

// Routes
app.post('/webhook/llm', llmHandler); // Legacy/Direct Chat
app.post('/webhook/tool', toolRunnerHandler); // Legacy/Direct Tool
app.post('/webhook/title', generateTitleHandler);

// New Nested Agent Routes
app.post('/webhook/orchestrator', orchestratorHandler);
app.post('/webhook/agent-init', agentInitHandler);
app.post('/webhook/agent-loop', agentLoopHandler);
app.post('/webhook/orchestrator-finalize', orchestratorFinalizeHandler);
app.post('/webhook/orchestrator-notify', orchestratorNotifyHandler);
app.post('/webhook/tool-runner', toolRunnerHandler); // Reusing existing tool runner for now, might need adaptation if sub-agent tool calls have different structure

app.get('/health', (req, res) => {
  res.send('OK');
});

app.listen(port, () => {
  console.log(`Trigger Worker listening at http://localhost:${port}`);
});
