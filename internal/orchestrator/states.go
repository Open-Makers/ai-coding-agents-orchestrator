package orchestrator

// PipelineState represents the current phase of the pipeline.
type PipelineState string

const (
	PipelineIdle        PipelineState = "idle"
	PipelinePM          PipelineState = "pm"
	PipelineNegotiating PipelineState = "negotiating"
	PipelineResearch    PipelineState = "research"
	PipelinePlanning    PipelineState = "planning"
	PipelineCoding      PipelineState = "coder"
	PipelinePoC         PipelineState = "poc"
	PipelineQATests     PipelineState = "qa_tests"
	PipelineQAReview    PipelineState = "qa_review"
	PipelineUXReviewing PipelineState = "ux_reviewer"
	PipelineSecurity    PipelineState = "security"
	PipelineDone        PipelineState = "done"
	PipelineGate        PipelineState = "human_gate"
	PipelineRateLimited PipelineState = "rate_limited"
)
