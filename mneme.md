## Mneme
The project is a library written in Golang.

## Overview
The idea is a system that allows a user to:
* define multiple data structures using JSON Schema + some JSON Schema extension. The definitions need to be stored.
  Definitions are identified by a mnemonic name, and can evolve over time, so a version field is required, and multiple
  versions need to be stored. The latest always win
* can insert data into a store. Each insert need to define which data structure it's using (by mnemonic name) and the
  data must conform to the latest version of the schema. The schema version is persisted with the record
* can load data, by listing (with pagination) or fetching an exact item by ID. In this case the data may conform to
  any versions of the schema. Filter should happen by any field
* Each record type should have an ID strategy definition (Ie. RID-5513). If none is provided, UUID will be used

## The Storage
* The library should not describe the storage. The storage is defined by the application including the library. So ideally
  Mneme offers some interfaces needing implementation and the enclosure must implement them, dependency-injection alike
* To better design Mneme, it's good to know that the storage is probably going to be a Postgres database or alike, with a
  lot of JSONB columns
* It is important to understand that how the storage is designed in terms of multi-tenancy, single column vs different
  columns, must be irrelevant to Mneme. The enclosure may decide that every domain object is a different table, or the same
  table with a disambiguation column

## The clues
* Ideally the enclosure will expose 2 MCP servers:
  * an MCP server that serves to describe and design the data structures
  * an MCP server that serves to query the data