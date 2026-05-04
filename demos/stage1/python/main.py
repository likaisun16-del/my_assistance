import os
from langchain.document_loaders import TextLoader
from langchain.text_splitter import CharacterTextSplitter
from langchain.embeddings import OpenAIEmbeddings
from langchain.vectorstores import FAISS
from langchain.llms import OpenAI
from langchain.chains import RetrievalQA

def main():
    # Load document
    loader = TextLoader("document.txt")
    documents = loader.load()

    # Split text
    text_splitter = CharacterTextSplitter(chunk_size=1000, chunk_overlap=0)
    docs = text_splitter.split_documents(documents)

    # Embeddings
    embeddings = OpenAIEmbeddings()

    # Vector store
    vectorstore = FAISS.from_documents(docs, embeddings)

    # LLM
    llm = OpenAI()

    # QA chain
    qa = RetrievalQA.from_chain_type(llm=llm, chain_type="stuff", retriever=vectorstore.as_retriever())

    # Query
    query = "What is the document about?"
    result = qa.run(query)
    print(result)

if __name__ == "__main__":
    main()