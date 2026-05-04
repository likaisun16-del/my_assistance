package main

import (
	"fmt"
	"log"

	"github.com/tmc/langchaingo/documentloaders"
	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/textsplitter"
	"github.com/tmc/langchaingo/vectorstores/faiss"
)

func main() {
	// Load document
	loader := documentloaders.NewText("document.txt")
	docs, err := loader.Load()
	if err != nil {
		log.Fatal(err)
	}

	// Split text
	splitter := textsplitter.NewRecursiveCharacter(1000, 0)
	splitDocs := splitter.SplitDocuments(docs)

	// Embeddings
	emb, err := embeddings.NewOpenAI()
	if err != nil {
		log.Fatal(err)
	}

	// Vector store
	store, err := faiss.New(emb, faiss.WithIndexName("index"))
	if err != nil {
		log.Fatal(err)
	}
	err = store.AddDocuments(splitDocs)
	if err != nil {
		log.Fatal(err)
	}

	// LLM
	llm, err := openai.New()
	if err != nil {
		log.Fatal(err)
	}

	// Retrieval QA (simplified)
	query := "What is the document about?"
	results, err := store.SimilaritySearch(query, 5)
	if err != nil {
		log.Fatal(err)
	}

	context := ""
	for _, doc := range results {
		context += doc.PageContent + "\n"
	}

	prompt := fmt.Sprintf("Based on the following context, answer the question: %s\n\nContext: %s", query, context)
	answer, err := llm.GenerateContent(prompt)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(answer)
}