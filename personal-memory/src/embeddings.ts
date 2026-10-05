export class EmbeddingClient {
  private readonly baseUrl = process.env.EMBEDDING_BASE_URL?.replace(/\/$/, "");
  private readonly instruction = process.env.EMBEDDING_QUERY_INSTRUCTION ?? "Retrieve memories and source events relevant to the user's query";

  get enabled() {
    return Boolean(this.baseUrl);
  }

  async embedDocuments(inputs: string[]) {
    return this.request(inputs);
  }

  async embedQuery(input: string) {
    const instructed = `Instruct: ${this.instruction}\nQuery: ${input}`;
    const result = await this.request([instructed]);
    return result[0];
  }

  private async request(inputs: string[]): Promise<number[][]> {
    if (!this.baseUrl) return [];
    const response = await fetch(`${this.baseUrl}/embed`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ inputs }),
    });
    if (!response.ok) throw new Error(`embedding service returned ${response.status}`);
    const payload = (await response.json()) as number[][] | { embeddings?: number[][] };
    const vectors = Array.isArray(payload) ? payload : payload.embeddings;
    if (!vectors?.length || !vectors.every((vector) => Array.isArray(vector) && vector.length > 0)) {
      throw new Error("embedding service returned an invalid vector response");
    }
    return vectors;
  }
}
