// Package graph builds and renders pure-Go, stdlib-only repository AST and
// behavioral telemetry constellations (HTML, JSON, Graphviz DOT, and Markdown;
// SDD M25, ADR-528). It never imports database/sql or modernc.org/sqlite.
package graph

import "encoding/json"

// Edge kind bitmasks for EdgeInput[4] and normalized edge tuples.
const (
	KindCall   = 1
	KindType   = 2
	KindIface  = 4
	KindImport = 8
)

// EdgeInput encodes [u, v, prodW, testW, kindMask].
type EdgeInput [5]int

// RecordLinks holds linked bug, ADR, and active memory counts and sorted IDs.
type RecordLinks struct {
	Bugs     int      `json:"bugs"`
	ADRs     int      `json:"adrs"`
	Memories int      `json:"memories"`
	IDs      []string `json:"ids,omitempty"`
}

// PackageInput is one package node supplied to Build.
type PackageInput struct {
	Path        string
	Layer       string
	FileCount   int
	SymbolCount int
	LineCount   int
	PageRank    float64
	Community   int
	InDegree    int
	OutDegree   int
	ClampHits   int
	RepeatReads int
	ChurnScore  int
	GodNode     bool
}

// FileInput is one file node supplied to Build.
type FileInput struct {
	Path        string
	Pkg         string
	PkgIdx      int
	Layer       string
	IsTest      bool
	IsDoc       bool
	SymbolCount int
	LineCount   int
	PageRank    float64
	Community   int
	InDegree    int
	OutDegree   int
	ClampHits   int
	RepeatReads int
	ChurnScore  int
	GodNode     bool
}

// SymbolInput is one AST declaration node supplied to Build.
type SymbolInput struct {
	Name      string
	QualName  string
	Recv      string
	Kind      string
	Pkg       string
	PkgIdx    int
	File      string
	FileIdx   int
	StartLine int
	EndLine   int
	Sig       string
	Exported  bool
	IsTest    bool
	PageRank  float64
	Community int
	InRefs    int
	OutRefs   int
	GodNode   bool
}

// HiddenCouplingInput is a co-activity file pair lacking a direct AST edge.
type HiddenCouplingInput struct {
	FileAIdx int
	FileBIdx int
	Count    int
	Kind     string
}

// Input is the pure-Go adapter boundary passed into Build.
type Input struct {
	RepoName       string
	Packages       []PackageInput
	Files          []FileInput
	Symbols        []SymbolInput
	PkgEdges       []EdgeInput
	FileEdges      []EdgeInput
	SymEdges       []EdgeInput
	PkgCycles      [][]int
	HiddenCoupling []HiddenCouplingInput
	FileLinks      map[string]RecordLinks
}

// HiddenCouplingTuple serializes as [uFileIdx, vFileIdx, count, kind] in JSON.
type HiddenCouplingTuple struct {
	U     int
	V     int
	Count int
	Kind  string
}

// MarshalJSON encodes HiddenCouplingTuple as a compact 4-element JSON array.
func (h HiddenCouplingTuple) MarshalJSON() ([]byte, error) {
	return json.Marshal([4]any{h.U, h.V, h.Count, h.Kind})
}

// UnmarshalJSON decodes [uFileIdx, vFileIdx, count, kind] into HiddenCouplingTuple.
func (h *HiddenCouplingTuple) UnmarshalJSON(data []byte) error {
	var raw [4]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[0], &h.U); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[1], &h.V); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[2], &h.Count); err != nil {
		return err
	}
	return json.Unmarshal(raw[3], &h.Kind)
}

// Meta holds repository-level counts, palette, layer colors, and package cycles.
type Meta struct {
	Repo          string            `json:"repo"`
	Palette       []string          `json:"palette"`
	LayerColors   map[string]string `json:"layerColors"`
	TotalPackages int               `json:"totalPackages"`
	ProdPackages  int               `json:"prodPackages"`
	TotalFiles    int               `json:"totalFiles"`
	ProdFiles     int               `json:"prodFiles"`
	TotalSymbols  int               `json:"totalSymbols"`
	ProdSymbols   int               `json:"prodSymbols"`
	PkgEdges      int               `json:"pkgEdges"`
	FileEdges     int               `json:"fileEdges"`
	SymEdges      int               `json:"symEdges"`
	PkgCycles     [][]int           `json:"pkgCycles"`
}

// PackageNode is one package in Payload.
type PackageNode struct {
	ID            string      `json:"id"`
	Label         string      `json:"label"`
	Layer         string      `json:"layer"`
	Lang          string      `json:"lang"`
	IsTest        bool        `json:"isTest,omitempty"`
	FileCount     int         `json:"fileCount"`
	ProdFileCount int         `json:"prodFileCount"`
	TestFileCount int         `json:"testFileCount"`
	SymbolCount   int         `json:"symbolCount"`
	ProdSymCount  int         `json:"prodSymCount"`
	LineCount     int         `json:"lineCount"`
	PageRank      float64     `json:"pr"`
	Community     int         `json:"comm"`
	InDegree      int         `json:"inDeg"`
	OutDegree     int         `json:"outDeg"`
	ProdInDeg     int         `json:"prodInDeg"`
	ProdOutDeg    int         `json:"prodOutDeg"`
	ClampHits     int         `json:"clampHits"`
	RepeatReads   int         `json:"repeatReads"`
	ChurnScore    int         `json:"churnScore"`
	GodNode       bool        `json:"godNode,omitempty"`
	X             float64     `json:"x"`
	Y             float64     `json:"y"`
	BrainLinks    RecordLinks `json:"brainLinks"`
}

// FileNode is one file in Payload.
type FileNode struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Pkg         string      `json:"pkg"`
	PkgIdx      int         `json:"pkgIdx"`
	Lang        string      `json:"lang"`
	Layer       string      `json:"layer"`
	IsTest      bool        `json:"isTest,omitempty"`
	IsDoc       bool        `json:"isDoc,omitempty"`
	SymbolCount int         `json:"symbolCount"`
	LineCount   int         `json:"lineCount"`
	PageRank    float64     `json:"pr"`
	Community   int         `json:"comm"`
	InDegree    int         `json:"inDeg"`
	OutDegree   int         `json:"outDeg"`
	ProdInDeg   int         `json:"prodInDeg"`
	ProdOutDeg  int         `json:"prodOutDeg"`
	ClampHits   int         `json:"clampHits"`
	RepeatReads int         `json:"repeatReads"`
	ChurnScore  int         `json:"churnScore"`
	GodNode     bool        `json:"godNode,omitempty"`
	X           float64     `json:"x"`
	Y           float64     `json:"y"`
	BrainLinks  RecordLinks `json:"brainLinks"`
}

// SymbolNode is one symbol in Payload.
type SymbolNode struct {
	Name       string  `json:"name"`
	QualName   string  `json:"qualName"`
	Recv       string  `json:"recv,omitempty"`
	Disp       string  `json:"disp"`
	Kind       string  `json:"kind"`
	Pkg        string  `json:"pkg"`
	PkgIdx     int     `json:"pkgIdx"`
	File       string  `json:"file"`
	FileIdx    int     `json:"fileIdx"`
	Lang       string  `json:"lang"`
	Layer      string  `json:"layer"`
	IsTest     bool    `json:"isTest,omitempty"`
	StartLine  int     `json:"start"`
	EndLine    int     `json:"end"`
	Sig        string  `json:"sig,omitempty"`
	Exported   bool    `json:"exported"`
	PageRank   float64 `json:"pr"`
	Community  int     `json:"comm"`
	InDegree   int     `json:"inDeg"`
	OutDegree  int     `json:"outDeg"`
	ProdInDeg  int     `json:"prodInDeg"`
	ProdOutDeg int     `json:"prodOutDeg"`
	GodNode    bool    `json:"godNode,omitempty"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
}

// Payload is the complete normalized constellation payload for JSON, HTML, DOT, and Markdown.
type Payload struct {
	Meta           Meta                   `json:"meta"`
	Packages       []PackageNode          `json:"packages"`
	Files          []FileNode             `json:"files"`
	Symbols        []SymbolNode           `json:"symbols"`
	PkgEdges       [][5]int               `json:"pkgEdges"`
	FileEdges      [][5]int               `json:"fileEdges"`
	SymEdges       [][5]int               `json:"symEdges"`
	HiddenCoupling []HiddenCouplingTuple  `json:"hiddenCoupling"`
	BrainLinks     map[string]RecordLinks `json:"brainLinks"`
}
