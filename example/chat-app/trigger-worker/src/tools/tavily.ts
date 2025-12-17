import axios from 'axios';

export class TavilyClient {
  private apiKey: string;

  constructor(apiKey: string) {
    this.apiKey = apiKey;
  }

  async search(query: string): Promise<string> {
    try {
      const response = await axios.post('https://api.tavily.com/search', {
        api_key: this.apiKey,
        query,
        search_depth: 'basic',
        include_answer: true,
        max_results: 3
      });

      // Return the answer if available, otherwise snippets
      if (response.data.answer) {
        return response.data.answer;
      }

      return JSON.stringify(response.data.results.map((r: any) => ({
        title: r.title,
        content: r.content,
        url: r.url
      })));
    } catch (error) {
      console.error('Tavily search failed:', error);
      return "Error performing search.";
    }
  }
}
