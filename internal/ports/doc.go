// Package ports declares the driving and driven boundaries of the
// application (repository-layout.md): the sink target interface
// (sink-adapter-contract.md), the durable dispatch surface the runtime
// consumes (E3-T2), and the source/filesystem/process/secrets/git ports
// owned by later roadmap tasks. Ports carry application-level types only;
// adapters implement them.
package ports
