import axios from 'axios';

export interface TavilySearchOptions {
  searchDepth?: 'basic' | 'advanced';
  maxResults?: number;
  includeDomains?: string[];
  excludeDomains?: string[];
  includeRawContent?: boolean;
}

export class TavilyClient {
  private apiKey: string;

  constructor(apiKey: string) {
    this.apiKey = apiKey;
  }

  async search(query: string, options: TavilySearchOptions = {}): Promise<string> {
    try {
      const response = await axios.post('https://api.tavily.com/search', {
        api_key: this.apiKey,
        query,
        search_depth: options.searchDepth || 'basic',
        include_answer: true,
        max_results: options.maxResults || 5,
        include_domains: options.includeDomains,
        exclude_domains: options.excludeDomains,
        include_raw_content: options.includeRawContent
      });

      const results = response.data.results.map((r: any) => ({
        title: r.title,
        url: r.url,
        content: r.content,
        score: r.score,
        rawContent: r.raw_content
      }));

      const output = {
        answer: response.data.answer,
        results: results
      };

      return JSON.stringify(output);
    } catch (error) {
      console.error('Tavily search failed:', error);
      return "Error performing search.";
    }
  }
}
