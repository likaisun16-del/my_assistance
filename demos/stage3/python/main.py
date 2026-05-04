from langchain.agents import initialize_agent, Tool
from langchain.llms import OpenAI
from langchain.chains import RetrievalQA
from langchain.vectorstores import FAISS
from langchain.embeddings import OpenAIEmbeddings
import requests

def search_baidu(query):
    # Simplified Baidu search
    return f"Search results for {query}"

def get_weather(city):
    # Simplified weather API
    return f"Weather in {city}: Sunny, 25°C"

def get_calendar():
    # Simplified calendar
    return "Today's events: Meeting at 10 AM"

def main():
    llm = OpenAI()

    # RAG setup (simplified)
    embeddings = OpenAIEmbeddings()
    vectorstore = FAISS.from_documents([], embeddings)  # Empty for demo
    qa = RetrievalQA.from_chain_type(llm=llm, chain_type="stuff", retriever=vectorstore.as_retriever())

    tools = [
        Tool(name="Search", func=search_baidu, description="Search the web using Baidu"),
        Tool(name="Weather", func=get_weather, description="Get weather information"),
        Tool(name="Calendar", func=get_calendar, description="Get calendar events"),
        Tool(name="QA", func=qa.run, description="Answer questions based on documents")
    ]

    agent = initialize_agent(tools, llm, agent="zero-shot-react-description")

    result = agent.run("What is the weather in Beijing and search for AI news?")
    print(result)

if __name__ == "__main__":
    main()