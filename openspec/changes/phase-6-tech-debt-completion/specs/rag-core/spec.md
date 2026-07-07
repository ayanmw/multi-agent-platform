## ADDED Requirements

### Requirement: Embedding Provider Interface
The system SHALL define an `EmbeddingProvider` interface for generating vector embeddings from text, abstracting the embedding model implementation.

#### Scenario: Generate embeddings for text
- **WHEN** the system needs to embed a text string
- **THEN** the EmbeddingProvider SHALL return a float32 vector and metadata (model, dimensions)

#### Scenario: Batch embedding generation
- **WHEN** multiple texts need embedding
- **THEN** the EmbeddingProvider SHALL support batch processing for efficiency

## ADDED Requirements

### Requirement: Vector Store Interface
The system SHALL define a `VectorStore` interface for storing and retrieving vectors with metadata, abstracting the vector database implementation.

#### Scenario: Store a vector with metadata
- **WHEN** a new embedding is created
- **THEN** the VectorStore SHALL store the vector with associated metadata (id, source, timestamp, scope)

#### Scenario: Search by similarity
- **WHEN** the system needs to retrieve relevant memories
- **THEN** the VectorStore SHALL return top-K vectors ranked by cosine similarity

#### Scenario: Delete by ID
- **WHEN** a memory is removed or expired
- **THEN** the VectorStore SHALL delete the corresponding vector and metadata

## ADDED Requirements

### Requirement: In-Memory Vector Store Implementation
The system SHALL provide an in-memory implementation of VectorStore for development and testing, using cosine similarity for search.

#### Scenario: In-memory search returns results
- **WHEN** the in-memory VectorStore has stored vectors
- **THEN** a search query SHALL return matching vectors within the configured similarity threshold
