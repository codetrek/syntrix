import 'dotenv/config';
import express from 'express';
import { llmHandler } from './handlers/llm';
import { toolRunnerHandler } from './handlers/tool-runner';

const app = express();
const port = process.env.PORT || 3000;

app.use(express.json());

// Routes
app.post('/webhook/llm', llmHandler);
app.post('/webhook/tool', toolRunnerHandler);

app.get('/health', (req, res) => {
  res.send('OK');
});

app.listen(port, () => {
  console.log(`Trigger Worker listening at http://localhost:${port}`);
});
