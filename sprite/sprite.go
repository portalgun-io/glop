package sprite

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	gl "github.com/chsc/gogl/gl21"
	"github.com/runningwild/glop/render"
	"github.com/runningwild/glop/util/algorithm"
	"github.com/runningwild/yedparse"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultFrameTime = 100
)

type spriteError struct {
	Msg string
}

func (e *spriteError) Error() string {
	return e.Msg
}

// attempt to make a relative path, otherwise leaves it alone
func tryRelPath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err == nil {
		return rel
	}
	return path
}

// utility function since we need to find the start node on any graph we use
func getStartNode(g *yed.Graph) *yed.Node {
	for i := 0; i < g.NumNodes(); i++ {
		if g.Node(i).Tag("mark") == "start" {
			return g.Node(i)
		}
	}
	return nil
}

// Valid state and anim graphs have the following properties:
// * All nodes are labeled
// * It has exactly one node that has the tag "mark" : "start"
// * All nodes in the graph can be reached by starting at the start node
// * All nodes and edges have only the specified tags
func verifyAnyGraph(graph *yed.Graph, node_tags, edge_tags []string) error {
	valid_node_tags := make(map[string]bool)
	for _, tag := range node_tags {
		valid_node_tags[tag] = true
	}

	valid_edge_tags := make(map[string]bool)
	for _, tag := range edge_tags {
		valid_edge_tags[tag] = true
	}

	// Check that all nodes have labels
	for i := 0; i < graph.NumNodes(); i++ {
		node := graph.Node(i)
		if node.NumLines() == 0 || strings.Contains(node.Line(0), ":") {
			return &spriteError{"contains an unlabeled node"}
		}
	}

	// Check that there is exactly one start node
	var start *yed.Node
	for i := 0; i < graph.NumNodes(); i++ {
		if graph.Node(i).Tag("mark") == "start" {
			if start == nil {
				start = graph.Node(i)
			} else {
				return &spriteError{"more than one node is marked as the start node"}
			}
		}
	}
	if start == nil {
		return &spriteError{"no start node was found"}
	}

	// Check that all nodes can be reached by the start node
	used := make(map[*yed.Node]bool)
	next := make(map[*yed.Node]bool)
	next[start] = true
	for len(next) > 0 {
		var nodes []*yed.Node
		for node := range next {
			nodes = append(nodes, node)
		}
		for _, node := range nodes {
			delete(next, node)
			used[node] = true
		}
		for _, node := range nodes {
			// Traverse the parent
			if node.Group() != nil && !used[node.Group()] {
				next[node.Group()] = true
			}
			// Traverse all the children
			for i := 0; i < node.NumChildren(); i++ {
				if !used[node.Child(i)] {
					next[node.Child(i)] = true
				}
			}
			// Traverse all outputs
			for i := 0; i < node.NumOutputs(); i++ {
				adj := node.Output(i).Dst()
				if !used[adj] {
					next[adj] = true
				}
			}
		}
	}
	if len(used) != graph.NumNodes() {
		return &spriteError{"not all nodes are reachable from the start node"}
	}

	// Check that nodes only have the specified tags
	for i := 0; i < graph.NumNodes(); i++ {
		node := graph.Node(i)
		for _, tag := range node.TagKeys() {
			if !(valid_node_tags[tag] || (node == start && tag == "mark")) {
				return &spriteError{fmt.Sprintf("a node has an unknown tag (%s)", tag)}
			}
		}
	}

	// Check that edges only have the specified tags
	for i := 0; i < graph.NumEdges(); i++ {
		edge := graph.Edge(i)
		for _, tag := range edge.TagKeys() {
			if !valid_edge_tags[tag] {
				return &spriteError{fmt.Sprintf("an edge has an unknown tag (%s)", tag)}
			}
		}
	}

	return nil
}

// A valid state graph has the following properties in addition to those
// specified in verifyAnyGraph():
// * All output edges from the start node have labels
// * No node has more than one unlabeled output edge
// * There are no tags on any nodes except for the start node
// * There are no groups
func verifyStateGraph(graph *yed.Graph) error {
	err := verifyAnyGraph(graph, []string{}, []string{"facing"})
	if err != nil {
		return &spriteError{fmt.Sprintf("State graph: %v", err)}
	}

	start := getStartNode(graph)

	// Check that all output edges from the start node have labels
	for i := 0; i < start.NumOutputs(); i++ {
		edge := start.Output(i)
		if edge.NumLines() == 0 || strings.Contains(edge.Line(0), ":") {
			return &spriteError{"State graph: The start node has an unlabeled output edge"}
		}
	}

	// Check that no node has more than one unlabeled output edge
	for i := 0; i < graph.NumNodes(); i++ {
		node := graph.Node(i)
		num_labels := 0
		for j := 0; j < node.NumOutputs(); j++ {
			edge := node.Output(j)
			if edge.NumLines() > 0 && !strings.Contains(edge.Line(0), ":") {
				num_labels++
			}
		}
		if num_labels < node.NumOutputs()-1 {
			return &spriteError{fmt.Sprintf("State graph: Found more than one unlabeled output edge on node '%s'", node.Line(0))}
		}
	}

	// Check that no nodes are groups
	for i := 0; i < graph.NumNodes(); i++ {
		node := graph.Node(i)
		if node.NumChildren() > 0 {
			return &spriteError{"State graph: cannot contain groups"}
		}
	}

	return nil
}

// A valid anim graph has the properties specified in verifyAnyGraph()
func verifyAnimGraph(graph *yed.Graph) error {
	err := verifyAnyGraph(graph, []string{"time", "sync", "func", "state"}, []string{"facing", "weight"})
	if err != nil {
		return &spriteError{fmt.Sprintf("Anim graph: %v", err)}
	}

	return nil
}

// Traverse the directory and do the following things:
// * There are n > 0 directories
// * There is at most 1 other file immediately within path - a thumb.png
// * All of the directories have names that are integers 0 - (n-1)
// * No image is present in any facing that isn't present in the anim graph
func verifyDirectoryStructure(path string, graph *yed.Graph) (num_facings int, filenames []string, err error) {
	filepath.Walk(path, func(cpath string, info os.FileInfo, _err error) error {
		if _err != nil {
			err = _err
			return err
		}
		if cpath == path {
			return nil
		}

		// skip hidden files
		if _, file := filepath.Split(cpath); file[0] == '.' {
			return nil
		}

		if info.IsDir() {
			num_facings++
			return filepath.SkipDir
		} else {
			switch {
			case info.Name() == "anim.xgml":
			case info.Name() == "state.xgml":
			case info.Name() == "thumb.png":
			case strings.HasSuffix(info.Name(), ".gob"):
			default:
				err = &spriteError{fmt.Sprintf("Unexpected file found in sprite directory, %s", tryRelPath(path, cpath))}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return
	}
	if num_facings == 0 {
		err = &spriteError{"Found no facings in the sprite directory"}
		return
	}

	// Create a set of valid png filenames.  If a .png shows up that is not in
	// this set then we raise an error.  Non-png files are allowed and are
	// ignored.
	valid_names := make(map[string]bool)
	for i := 0; i < graph.NumNodes(); i++ {
		valid_names[graph.Node(i).Line(0)+".png"] = true
	}

	filenames_map := make(map[string]bool)
	for facing := 0; facing < num_facings; facing++ {
		cur := filepath.Join(path, fmt.Sprintf("%d", facing))
		filepath.Walk(cur, func(cpath string, info os.FileInfo, _err error) error {
			if _err != nil {
				err = _err
				return err
			}
			if cpath == cur {
				return nil
			}

			// skip hidden files
			if _, file := filepath.Split(cpath); file[0] == '.' {
				return nil
			}

			if info.IsDir() {
				err = &spriteError{fmt.Sprintf("Found a directory inside facing directory %d, %s", facing, tryRelPath(path, cpath))}
				return err
			}
			if filepath.Ext(cpath) == ".png" {
				base := filepath.Base(cpath)
				if valid_names[base] {
					filenames_map[base] = true
				} else {
					err = &spriteError{fmt.Sprintf("Found an unused .png file: %s", tryRelPath(path, cpath))}
				}
				return err
			}
			return nil
		})
	}

	for filename := range filenames_map {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)

	return
}

// Used to determine what frames to keep permanently in texture memory, and
// which ones to unload when not needed
type animAlgoGraph struct {
	anim *yed.Graph
}

func (cg *animAlgoGraph) NumVertex() int {
	return cg.anim.NumNodes()
}
func (cg *animAlgoGraph) Adjacent(n int) (adj []int, cost []float64) {
	node := cg.anim.Node(n)

	var delay float64 = defaultFrameTime
	if node.Tag("time") != "" {
		t, err := strconv.ParseFloat(node.Tag("time"), 64)
		if err == nil {
			delay = t
		} else {
			// TODO: Should log this as a warning or something
		}
	}
	for i := 0; i < node.NumGroupOutputs(); i++ {
		edge := node.GroupOutput(i)
		adj = append(adj, edge.Dst().Id())

		// frames that are part of groups can be cancelled at any time if the
		// animation is supposed to proceed out of the group, so if an edge leads
		// away from the current group we will assume that it has a delay of 0.
		if node.Group() != nil && edge.Dst().Group() != node.Group() {
			cost = append(cost, 0)
		} else {
			cost = append(cost, delay)
		}
	}
	return
}

type waiter struct {
	// Any state mentioned here is acceptable to signal the waiter
	states []string

	c chan struct{}
}

func (s *Sprite) Wait(states []string) {
	s.waiter_mutex.Lock()
	var w waiter
	w.states = states
	w.c = make(chan struct{}, 1)
	s.waiters = append(s.waiters, &w)
	s.waiter_mutex.Unlock()
	<-w.c
}

type Sprite struct {
	shared     *sharedSprite
	anim_node  *yed.Node
	state_node *yed.Node

	// Used to run callbacks when certain frames of animations are hit.
	trigger TriggerFunc

	// number of times Think() has been called.  This is mostly so that we can
	// run some code the very first time that Think() is called.
	thinks int

	// current facing - needed to index into the appropriate sheet in shared
	facing int

	// previous facing - tracking this lets us prevent having to load/unload
	// lots of facings if a sprite changes facings multiple times between thinks
	prev_facing int

	// current facing in the state graph.  This lets us know what direction the
	// sprite will be facing once it is done with all of its pendings commands
	// and its current path.
	state_facing int

	// Time remaining on the current frame of animation
	togo int64

	// If len(path) > 0 then this is the series of animation frames that will be
	// used next
	path []*yed.Node

	// commands that have been accepted by the state graph but haven't been
	// processed by the anim graph.  When path is empty a cmd will be taken from
	// this list and be used to generate the next path.
	pending_cmds []command

	waiter_mutex sync.Mutex
	waiters      []*waiter
}

type command struct {
	names []string // List of names of edges

	group *commandGroup
}

type commandGroup struct {
	// This is the tag that all of the sprites in this group will sync to
	sync_tag string

	// all of the sprites in this list must have this commandGroup as part of
	// their next command to execute before any of them will execute it.
	sprites []*Sprite

	// If ready() ever returns true then this will be set to true and read()
	// will always return true after that.  This prevents a situation where one
	// sprite starts executing this command and then other sprites think they
	// aren't ready because one of them has already progressed passed this
	// command.
	was_ready bool

	// Map from sprite to how long the sprite should wait until starting this
	// command.  This map is not created until all sprites are ready.
	eta   map[*Sprite]int64
	paths map[*Sprite][]*yed.Node
}

// Returns true iff all sprites in this group have no pending cmds before this
// one, and no nodes remaining in their immediate path.
func (cg *commandGroup) ready() bool {
	if cg.was_ready {
		return true
	}
	for _, sp := range cg.sprites {
		if len(sp.path) > 0 {
			return false
		}
		if len(sp.pending_cmds) == 0 {
			return false
		} // This one is a serious problem
		if sp.pending_cmds[0].group != cg {
			return false
		}
	}
	// Everyone is ready, so we'll check how long it's going to take each one to
	// get to the sync node and save that data.
	cg.eta = make(map[*Sprite]int64)
	cg.paths = make(map[*Sprite][]*yed.Node)
	var max int64
	for _, sp := range cg.sprites {
		path := sp.findPathForSyncedCmd(sp.pending_cmds[0], sp.anim_node)
		var total int64
		for i, node := range path {
			if node.Tag("sync") == cg.sync_tag {
				break
			}
			var prev *yed.Node
			if i == 0 {
				prev = sp.anim_node
			} else {
				prev = path[i-1]
			}
			if !connectedByGroupEdge(prev, node) {
				if i == 0 {
					total += sp.togo
				} else {
					total += sp.shared.node_data[node].time
				}
			}
		}
		cg.eta[sp] = total
		cg.paths[sp] = path
		if total > max {
			max = total
		}
	}
	for _, sp := range cg.sprites {
		cg.eta[sp] = max - cg.eta[sp]
	}
	cg.was_ready = true
	return true
}

func (s *Sprite) State() string {
	return s.state_node.Line(0)
}
func (s *Sprite) Anim() string {
	return s.anim_node.Line(0)
}
func (s *Sprite) AnimState() string {
	return s.shared.node_data[s.anim_node].state
}

func connectedByGroupEdge(n1, n2 *yed.Node) bool {
	for i := 0; i < n1.NumGroupOutputs(); i++ {
		edge := n1.GroupOutput(i)
		if edge.Src() != n1 && edge.Dst() == n2 {
			return true
		}
	}
	return false
}

// selects an outgoing edge from node random among those outgoing edges that
// have cmd listed in cmds.  The random choice is weighted by the weights
// found in edge_data
func selectAnEdge(node *yed.Node, edge_data map[*yed.Edge]edgeData, cmds []string) *yed.Edge {
	cmd_map := make(map[string]bool)
	for _, cmd := range cmds {
		cmd_map[cmd] = true
	}

	total := 0.0
	for i := 0; i < node.NumOutputs(); i++ {
		edge := node.Output(i)
		if _, ok := cmd_map[edge_data[edge].cmd]; !ok {
			continue
		}
		total += edge_data[edge].weight
	}
	if total > 0 {
		pick := rand.Float64() * total
		total = 0.0
		for i := 0; i < node.NumOutputs(); i++ {
			edge := node.Output(i)
			if _, ok := cmd_map[edge_data[edge].cmd]; !ok {
				continue
			}
			total += edge_data[edge].weight
			if total >= pick {
				return edge
			}
		}
	}
	return nil
}

// Returns the edge that leads from a, or an ancestor of a, to b, or an
// ancestor of b
func edgeTo(a, b *yed.Node) *yed.Edge {
	for i := 0; i < a.NumGroupOutputs(); i++ {
		edge := a.GroupOutput(i)
		for cb := b; cb != nil; cb = cb.Group() {
			if edge.Dst() == cb {
				return edge
			}
		}
	}
	return nil
}

func CommandSync(ss []*Sprite, cmds [][]string, sync_tag string) {
	// Go through each sprite, if it can execute the specified command then add
	// it to the group (and if it can't, don't).
	var group commandGroup
	group.sync_tag = sync_tag
	for i := range ss {
		cmd := command{
			names: cmds[i],
			group: &group,
		}
		if ss[i].baseCommand(cmd) {
			group.sprites = append(group.sprites, ss[i])
		}
	}
}

func (s *Sprite) baseCommand(cmd command) bool {
	state_node := s.state_node
	for _, name := range cmd.names {
		state_edge := selectAnEdge(state_node, s.shared.edge_data, []string{name})
		if state_edge == nil {
			return false
		}
		state_node = state_edge.Dst()
	}
	for _, name := range cmd.names {
		edge := selectAnEdge(s.state_node, s.shared.edge_data, []string{name})
		s.state_node = edge.Dst()
		face := s.shared.edge_data[edge].facing
		s.state_facing = (s.state_facing + face + len(s.shared.facings)) % len(s.shared.facings)
	}

	state_edge := selectAnEdge(s.state_node, s.shared.edge_data, []string{""})
	for state_edge != nil {
		// If this command is synced then we first need to make sure that we'll
		// be able to get to the appropriate sync tag
		// if cmd.group != nil && cmd.group.sync_tag != "" {
		//   dst := state_edge.Dst()
		//   s.shared.node_data
		// }
		s.state_node = state_edge.Dst()
		state_edge = selectAnEdge(s.state_node, s.shared.edge_data, []string{""})
	}

	s.pending_cmds = append(s.pending_cmds, cmd)
	return true
}

func (s *Sprite) NumPendingCmds() int {
	return len(s.pending_cmds)
}

func (s *Sprite) Idle() bool {
	return len(s.pending_cmds) == 0 && len(s.path) == 0
}

func (s *Sprite) Command(cmd string) {
	s.baseCommand(command{names: []string{cmd}, group: nil})
}

func (s *Sprite) CommandN(cmds []string) {
	s.baseCommand(command{names: cmds, group: nil})
}

// This is a specialized wrapper around a yed.Graph that allows for the start
// node to be differentiated from the ending node in a path in the event that
// they are the same node in the original graph.  This means that if a path is
// requested from one node to the same node that the resulting path will not
// be length 0.
type pathingGraph struct {
	shared *sharedSprite
	// graph *yed.Graph
	start *yed.Node

	// Edges will only be followed if there is no command associated with them,
	// or if the command associated with them is the same as this command.
	cmd string
}

func (p pathingGraph) NumVertex() int {
	return p.shared.anim.NumNodes() + 1
}
func (p pathingGraph) Adjacent(n int) (adj []int, cost []float64) {
	var node *yed.Node
	if n == p.shared.anim.NumNodes() {
		node = p.start
	} else {
		node = p.shared.anim.Node(n)
	}
	for i := 0; i < node.NumGroupOutputs(); i++ {
		edge := node.GroupOutput(i)
		if p.shared.edge_data[edge].cmd != "" && p.shared.edge_data[edge].cmd != p.cmd {
			continue
		}
		adj = append(adj, edge.Dst().Id())
		cost = append(cost, 1)
	}
	return
}

func (s *Sprite) SetTriggerFunc(tf TriggerFunc) {
	s.trigger = tf
}

// Like findPathForCmd, but extends the path, if necessary, such that a node
// with the appropriate sync_tag is in the path.  If such a node cannot be
// found then no additional nodes are added to the path.
func (s *Sprite) findPathForSyncedCmd(cmd command, anim_node *yed.Node) []*yed.Node {
	path := s.findPathForCmd(cmd, anim_node)
	if len(path) == 0 {
		return path
	}
	for _, node := range path {
		if node.Tag("sync") == cmd.group.sync_tag {
			return path
		}
	}
	var extra []*yed.Node
	adds := make(map[*yed.Node]bool)
	tail := path[len(path)-1]
	edge := selectAnEdge(tail, s.shared.edge_data, []string{""})
	for !adds[tail] && edge != nil {
		adds[tail] = true
		tail = edge.Dst()
		extra = append(extra, tail)
		if tail.Tag("sync") == cmd.group.sync_tag {
			break
		}
		edge = selectAnEdge(tail, s.shared.edge_data, []string{""})
	}
	if len(extra) > 0 && extra[len(extra)-1].Tag("sync") == cmd.group.sync_tag {
		for _, node := range extra {
			path = append(path, node)
		}
	}
	return path
}

// If this returns a path with length 0 it means there wasn't a valid path
func (s *Sprite) findPathForCmd(cmd command, anim_node *yed.Node) []*yed.Node {
	var node_path []*yed.Node
	for _, name := range cmd.names {
		g := pathingGraph{shared: s.shared, start: anim_node, cmd: name}
		var end []int
		for i := 0; i < s.shared.anim.NumEdges(); i++ {
			edge := s.shared.anim.Edge(i)
			if s.shared.edge_data[edge].cmd == name {
				end = append(end, edge.Dst().Id())
			}
		}
		_, path := algorithm.Dijkstra(g, []int{s.shared.anim.NumNodes()}, end)
		for _, id := range path[1:] {
			node_path = append(node_path, s.shared.anim.Node(id))
		}
		if len(node_path) > 0 {
			anim_node = node_path[len(node_path)-1]
		}
	}

	return node_path
}

func (s *Sprite) applyPath(path []*yed.Node) {
	for _, n := range path {
		s.path = append(s.path, n)
	}
}

func (s *Sprite) Dims() (dx, dy int) {
	var rect FrameRect
	var ok bool
	fid := frameId{facing: s.facing, node: s.anim_node.Id()}
	rect, ok = s.shared.connector.rects[fid]
	if !ok {
		rect, ok = s.shared.facings[s.facing].rects[fid]
		if !ok {
			return 0, 0
		}
	}
	dx = rect.X2 - rect.X
	dy = rect.Y2 - rect.Y
	return
}

func (s *Sprite) Bind() (x, y, x2, y2 float64) {
	var rect FrameRect
	var sh *sheet
	var ok bool
	fid := frameId{facing: s.facing, node: s.anim_node.Id()}
	var dx, dy float64
	if rect, ok = s.shared.connector.rects[fid]; ok {
		sh = s.shared.connector
	} else if rect, ok = s.shared.facings[s.facing].rects[fid]; ok {
		sh = s.shared.facings[s.facing]
	} else {
		gl.BindTexture(gl.TEXTURE_2D, error_texture)
		return
	}
	gl.BindTexture(gl.TEXTURE_2D, sh.texture)
	dx = float64(sh.dx)
	dy = float64(sh.dy)
	x = float64(rect.X) / dx
	y = float64(rect.Y) / dy
	x2 = float64(rect.X2) / dx
	y2 = float64(rect.Y2) / dy
	return
}
func (s *Sprite) Facing() int {
	return s.facing
}
func (s *Sprite) StateFacing() int {
	return s.state_facing
}
func (s *Sprite) doTrigger() {
	if s.trigger != nil &&
		s.anim_node.Tag("func") != "" {
		s.trigger(s, s.anim_node.Tag("func"))
	}
}

type spriteStateInternal struct {
	Facing        int
	State_node_id int
	Anim_node_id  int
}

// An opaque object that contains everything necessary to start a sprite from
// a particular point.  Useful when rewinding something, for example.
// Gobbable.
type SpriteState struct {
	internals spriteStateInternal
}

func (ss *SpriteState) GobEncode() ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(ss.internals)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (ss *SpriteState) GobDecode(data []byte) error {
	return gob.NewDecoder(bytes.NewBuffer(data)).Decode(&ss.internals)
}

func (s *Sprite) GetSpriteState() SpriteState {
	return SpriteState{
		internals: spriteStateInternal{
			Facing:        s.facing,
			State_node_id: s.state_node.Id(),
			Anim_node_id:  s.anim_node.Id(),
		},
	}
}

func (s *Sprite) SetSpriteState(state SpriteState) error {
	if len(s.waiters) != 0 {
		return errors.New("Can't SetSpriteState while there are pending waiters.")
	}
	if s.thinks == 0 {
		s.prev_facing = s.facing
		s.facing = state.internals.Facing
		s.state_facing = s.facing
		s.shared.facings[s.facing].Load()
	} else if state.internals.Facing != s.facing {
		// s.shared.facings[s.facing].Unload()
		s.facing = state.internals.Facing
		s.state_facing = s.facing
		s.shared.facings[s.facing].Load()
	}
	s.anim_node = s.shared.anim.Node(state.internals.Anim_node_id)
	s.state_node = s.shared.state.Node(state.internals.State_node_id)
	s.path = nil
	s.pending_cmds = nil
	return nil
}

func (s *Sprite) Think(dt int64) {
	if s.thinks == 0 {
		s.shared.facings[0].Load()
		s.togo = s.shared.node_data[s.anim_node].time
	}
	s.thinks++
	if dt < 0 {
		return
		// panic("Can't have dt < 0")
	}

	// Check for waiters
	defer func() {
		if s.NumPendingCmds() > 0 {
			return
		}
		for i := range s.waiters {
			for _, state := range s.waiters[i].states {
				if state == s.AnimState() {
					s.waiters[i].c <- struct{}{}
					s.waiters[i].states = nil
				}
			}
			algorithm.Choose(&s.waiters, func(w *waiter) bool {
				return w.states != nil
			})
		}
	}()

	var path []*yed.Node
	if len(s.pending_cmds) > 0 && len(s.path) == 0 {
		if s.pending_cmds[0].group == nil {
			path = s.findPathForCmd(s.pending_cmds[0], s.anim_node)
		} else if s.pending_cmds[0].group.ready() {
			t := s.pending_cmds[0].group.eta[s]
			t -= dt
			if t <= 0 {
				path = s.pending_cmds[0].group.paths[s]
				s.anim_node = path[0]
				s.doTrigger()
				s.togo = s.shared.node_data[s.anim_node].time
				path = path[1:]
			}
			s.pending_cmds[0].group.eta[s] = t
		}
	}
	if path != nil {
		s.applyPath(path)
		s.pending_cmds = s.pending_cmds[1:]
	}

	if len(s.path) > 0 && s.anim_node.Group() != nil {
		// If the current node is in a group that has an edge to the next node
		// then we want to follow that edge immediately rather than waiting for
		// the time for this frame to elapse
		for i := 0; i < s.anim_node.NumGroupOutputs(); i++ {
			edge := s.anim_node.GroupOutput(i)
			if edge.Src() == s.anim_node {
				continue
			}
			if edge.Dst() == s.path[0] {
				s.togo = 0
			}
		}
	}
	if s.togo >= dt {
		s.togo -= dt
		if s.facing != s.prev_facing {
			s.shared.facings[s.prev_facing].Unload()
			s.shared.facings[s.facing].Load()
			s.prev_facing = s.facing
		}
		return
	}
	dt -= s.togo
	var next *yed.Node
	if len(s.path) > 0 {
		next = s.path[0]
		s.path = s.path[1:]
	} else {
		edge := selectAnEdge(s.anim_node, s.shared.edge_data, []string{""})
		if edge != nil {
			next = edge.Dst()
		} else {
			next = s.anim_node
		}
	}
	var edge *yed.Edge
	if next != nil {
		edge = edgeTo(s.anim_node, next)
		face := s.shared.edge_data[edge].facing
		if face != 0 {
			s.facing = (s.facing + face + len(s.shared.facings)) % len(s.shared.facings)
		}
	}
	s.anim_node = next
	s.doTrigger()
	s.togo = s.shared.node_data[s.anim_node].time
	s.Think(dt)
}

type nodeData struct {
	time     int64
	sync_tag string

	// The state that this frame of animation belongs to
	state string
}
type edgeData struct {
	facing int
	weight float64
	cmd    string
}
type Data struct {
	state *yed.Node
	anim  *yed.Node
}
type FrameRect struct {
	X, Y, X2, Y2 int
}

// A trigger func is a function that is called when a certain frame of
// animation is reached.  It is specified by line like "func:foo bar wingding"
// in the animation graph, and such a line will mean that when that frame is
// reached by any sprite with that graph the TriggerFunc foo will be called
// with two parameters, the Sprite that reached that frame, and the text
// "foo bar wingding".
type TriggerFunc func(*Sprite, string)

type Manager struct {
	shared map[string]*sharedSprite
	mutex  sync.Mutex
}

func MakeManager() *Manager {
	var m Manager
	m.shared = make(map[string]*sharedSprite)
	return &m
}

var the_manager *Manager
var error_texture gl.Uint
var gen_tex_once sync.Once

func init() {
	the_manager = MakeManager()
}
func LoadSprite(path string) (*Sprite, error) {
	return the_manager.LoadSprite(path)
}
func (m *Manager) loadSharedSprite(path string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if _, ok := m.shared[path]; ok {
		return nil
	}

	ss, err := loadSharedSprite(path)
	if err != nil {
		return err
	}
	m.shared[path] = ss
	ss.manager = m
	return nil
}

func (m *Manager) LoadSprite(path string) (*Sprite, error) {
	// We can't run this during an init() function because it will get queued to
	// run before the opengl context is created, so we just check here and run
	// it if we haven't run it before.
	gen_tex_once.Do(func() {
		render.Queue(func() {
			gl.Enable(gl.TEXTURE_2D)
			gl.GenTextures(1, &error_texture)
			gl.BindTexture(gl.TEXTURE_2D, error_texture)
			gl.TexEnvf(gl.TEXTURE_ENV, gl.TEXTURE_ENV_MODE, gl.MODULATE)
			gl.TexParameterf(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
			gl.TexParameterf(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
			gl.TexParameterf(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.REPEAT)
			gl.TexParameterf(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.REPEAT)
			pink := []byte{255, 0, 255, 255}
			gl.TexImage2D(
				gl.TEXTURE_2D,
				0,
				gl.RGBA,
				1,
				1,
				0,
				gl.RGBA,
				gl.UNSIGNED_INT,
				gl.Pointer(&pink[0]))
		})
	})

	path = filepath.Clean(path)
	err := m.loadSharedSprite(path)
	if err != nil {
		return nil, err
	}
	var s Sprite
	m.mutex.Lock()
	s.shared = m.shared[path]
	m.mutex.Unlock()
	s.anim_node = s.shared.anim_start
	s.state_node = s.shared.state_start
	return &s, nil
}
