# DuckStream API Reference

## Base URL

```
http://localhost:8080
```

## Endpoints

### List Queries

Get all registered queries.

```http
GET /queries
```

**Response:**
```json
{
  "queries": [
    {
      "id": "q1",
      "sql": "SELECT * FROM events",
      "active": true
    }
  ]
}
```

---

### Register Query

Register a new streaming query.

```http
POST /queries
Content-Type: application/json
```

**Request Body:**
```json
{
  "id": "q1",
  "sql": "SELECT * FROM events WHERE data->>'event' = 'click'"
}
```

**Success Response:**
```json
{
  "status": "ok",
  "id": "q1"
}
```

**Error Responses:**
```json
{"error": "query already exists"}
```
```json
{"error": "invalid query: table does not exist"}
```
```json
{"error": "max queries reached"}
```

---

### Unregister Query

Stop and remove a query.

```http
DELETE /queries/{id}
```

**Success Response:**
```json
{
  "status": "ok",
  "id": "q1"
}
```

**Error Response:**
```json
{"error": "query not found"}
```

---

### Get Metrics

Get system metrics.

```http
GET /metrics
```

**Response:**
```json
{
  "queries_registered": 10,
  "queries_unregistered": 2,
  "rows_sent": 1500,
  "errors": 0,
  "active_clients": 3,
  "active_queries": 8
}
```

---

### Ingest Event

Insert an event into the events table.

```http
POST /ingest
Content-Type: application/json
```

**Request Body:**
```json
{
  "data": "{\"event\":\"click\",\"x\":100,\"y\":200}"
}
```

**Success Response:**
```json
{"status": "ok"}
```

---

### Stream Events (SSE)

Subscribe to event stream.

```http
GET /stream/events
```

**Response (Server-Sent Events):**
```
data: {"ingestion_rate": 15, "timestamp": "2026-04-02T12:00:00Z"}

data: {"ingestion_rate": 18, "timestamp": "2026-04-02T12:00:01Z"}
```

---

### Stream Query (SSE)

Subscribe to a specific query's results.

```http
GET /stream/queries/{id}
```

**Response (Server-Sent Events):**
```
data: {"id": "q1", "sql": "SELECT * FROM events", "active": true, "status": "running"}
```

---

## cURL Examples

```bash
# List queries
curl http://localhost:8080/queries

# Register a query
curl -X POST http://localhost:8080/queries \
  -H "Content-Type: application/json" \
  -d '{"id":"clicks","sql":"SELECT * FROM events WHERE data->>'\''event'\'' = '\''click'\''"}'

# Register query with JSON filter
curl -X POST http://localhost:8080/queries \
  -H "Content-Type: application/json" \
  -d '{"id":"q1","sql":"SELECT id, data, timestamp FROM events WHERE id > 0"}'

# Unregister query
curl -X DELETE http://localhost:8080/queries/q1

# Get metrics
curl http://localhost:8080/metrics

# Ingest event
curl -X POST http://localhost:8080/ingest \
  -H "Content-Type: application/json" \
  -d '{"data":"{\"event\":\"click\",\"x\":42,\"y\":100}"}'

# Stream events
curl -N http://localhost:8080/stream/events
```

## Error Handling

All error responses include a descriptive message:

```json
{"error": "description of the error"}
```

Common error codes:
- `400 Bad Request` - Invalid request format or parameters
- `404 Not Found` - Query does not exist
- `503 Service Unavailable` - Max clients/streams reached