/*
Package cdt provides an implementation of 2-dimensional Constrained Delaunay
Triangulation.  The implementation follows the algorithm outlined at
http://www.cescg.org/CESCG-2004/web/Domiter-Vid/index.html, but without any of
the spatial data structures.
*/
package cdt

// #cgo CXXFLAGS: -O3 -std=c++11 -Wall -Werror
// #include <stdlib.h>
// int triangulate(float left, float right, float bottom, float top,
//                 int nPoints, float *points, int nEdges, int *edges,
//                 float *verts, int *srcToDstIs, int *triangles);
import "C"

import (
	"fmt"
	"github.com/go-gl/mathgl/mgl32"
	"math"
	"sort"
	"unsafe"
)

func Triangulate(left, right, bottom, top float32,
	points []float32, edges []int32) (verts []float32, srcToDstIs []int32, triangles []int32) {

	cPoints := unsafe.Pointer(C.malloc(C.size_t(len(points) * 4)))
	for i, f := range points {
		p := (*float32)(unsafe.Pointer(uintptr(cPoints) + uintptr(i*4)))
		*p = f
	}
	cEdges := unsafe.Pointer(C.malloc(C.size_t(len(points) * 4)))
	for i, e := range edges {
		p := (*int32)(unsafe.Pointer(uintptr(cEdges) + uintptr(i*4)))
		*p = e
	}

	numPoints := (len(points) / 2) + 4
	srcToDstIs = make([]int32, len(points)/2)
	verts = make([]float32, numPoints*2)
	triangles = make([]int32, 3*(2*numPoints-6))
	cSrcToDstIs := unsafe.Pointer(C.malloc(C.size_t(len(srcToDstIs) * 4)))
	cVerts := unsafe.Pointer(C.malloc(C.size_t(len(verts) * 4)))
	cTriangles := unsafe.Pointer(C.malloc(C.size_t(len(triangles) * 4)))
	// fmt.Printf("triangulate(%ff, %ff, %ff, %ff, %d, %v, %d, %v, verts, triangles)\n",
	// 	left, right, bottom, top, len(points)/2, points, len(edges)/2, edges)
	err := C.triangulate(
		C.float(left), C.float(right), C.float(bottom), C.float(top),
		C.int(int32(len(points)/2)), (*C.float)(cPoints),
		C.int(int32(len(edges)/2)), (*C.int)(cEdges),
		(*C.float)(cVerts), (*C.int)(cSrcToDstIs), (*C.int)(cTriangles))
	if err < 0 {
		panic(fmt.Sprintf("triangulate failed with error %d", err))
	}
	C.free(cPoints)
	C.free(cEdges)
	for i := 0; i < len(srcToDstIs); i++ {
		p := (*int32)(unsafe.Pointer(uintptr(cSrcToDstIs) + uintptr(i*4)))
		srcToDstIs[i] = *p
	}
	C.free(cSrcToDstIs)
	for i := 0; i < len(verts); i++ {
		p := (*float32)(unsafe.Pointer(uintptr(cVerts) + uintptr(i*4)))
		verts[i] = *p
	}
	C.free(cVerts)
	for i := 0; i < len(triangles); i++ {
		p := (*int32)(unsafe.Pointer(uintptr(cTriangles) + uintptr(i*4)))
		triangles[i] = *p
	}
	C.free(cTriangles)
	// truncate the result, in case of duplicates:
	numPoints = int(err)
	verts = verts[:numPoints]
	triangles = triangles[:3*(numPoints-6)]
	return verts, srcToDstIs, triangles
}

type Triangulation struct {
	Verts []mgl32.Vec2
	// Edge pairs are always stored with the smaller vert index first
	Edges []int
	// Triangles are always stored with clockwise winding
	Triangles []int
	// This isn't currently enforced, but could be useful for error checking
	fixed []bool

	// Used for edge insertion:
	newTris, newEdges []int
	checkTris         []int
	// Indices:
	VertI, EdgeI, TriangleI      int
	newTriI, newEdgeI, checkTriI int
}

// NewTriangulation returns a new Triangulation initialized for performing
// triangulation of numPoints points that exist within the region defined by
// left and right on the X axis and by bottom and top on the Y axis.  Points
// added to the triangulation should exist within the boundary defined by left,
// right, bottom, and top, and they should not fall directly on the edges of or
// outside of this rectangle.
func NewTriangulation(left, right, bottom, top float32, numPoints int) *Triangulation {
	result := &Triangulation{}
	numPoints += 4
	// Preallocate everything according to maximum theoretical size
	result.Verts = make([]mgl32.Vec2, numPoints)
	result.Verts[0] = mgl32.Vec2{left, bottom}
	result.Verts[1] = mgl32.Vec2{left, top}
	result.Verts[2] = mgl32.Vec2{right, bottom}
	result.Verts[3] = mgl32.Vec2{right, top}
	result.VertI = 4
	result.Edges = make([]int, 2*(3*numPoints-7))
	result.Edges[0] = 0
	result.Edges[1] = 1
	result.Edges[2] = 0
	result.Edges[3] = 2
	result.Edges[4] = 1
	result.Edges[5] = 2
	result.Edges[6] = 1
	result.Edges[7] = 3
	result.Edges[8] = 2
	result.Edges[9] = 3
	result.EdgeI = 10
	result.Triangles = make([]int, 3*(2*numPoints-6))
	result.Triangles[0] = 0
	result.Triangles[1] = 1
	result.Triangles[2] = 2
	result.Triangles[3] = 2
	result.Triangles[4] = 1
	result.Triangles[5] = 3
	result.TriangleI = 6
	result.fixed = make([]bool, 3*numPoints-7)
	result.fixed[0] = true
	result.fixed[1] = true
	result.fixed[2] = false
	result.fixed[3] = true
	result.fixed[4] = true
	result.newTris = make([]int, 3*(2*numPoints-6))
	result.newEdges = make([]int, 2*(3*numPoints-7))
	result.checkTris = make([]int, 3*numPoints-7)
	return result
}

// AddPoint inserts the point defined by x and y in to the triangulation.  The
// returned index can be used to add edges involving this point to the
// constrained triangulation after all points have been added.
func (t *Triangulation) AddPoint(x, y float32) (index int) {
	pt := mgl32.Vec2{x, y}
	// find our encompassing triangle: (linear search cause honestly)
	duplicate := false
	found := false
	parentTriIs := [2]int{-1, -1}
	parentTriIsIdx := 0

	minVertDist := float32(math.MaxFloat32)
	minVertI := -1
	for i := 0; i < t.VertI; i++ {
		v := pt.Sub(t.Verts[i])
		vertDist := v[0]*v[0] + v[1]*v[1]
		if vertDist < minVertDist {
			minVertI = i
			minVertDist = vertDist
		}
	}

	// note: making this a function to avoid copypasta kills perf
	// note: just use c
	for i := 0; i < t.TriangleI; i += 3 {
		if t.Triangles[i] != minVertI &&
			t.Triangles[i+1] != minVertI &&
			t.Triangles[i+2] != minVertI {

			continue
		}
		if pointInTriangle(pt,
			t.Verts[t.Triangles[i]],
			t.Verts[t.Triangles[i+1]],
			t.Verts[t.Triangles[i+2]]) {
			found = true
			if parentTriIsIdx < 2 {
				parentTriIs[parentTriIsIdx] = i
				parentTriIsIdx++
			} else {
				duplicate = true
				break
			}
		}
	}
	if !found {
		vertDists := make([]float32, t.VertI)
		sortedVertIs := make([]int, t.VertI)
		for i := 0; i < t.VertI; i++ {
			v := pt.Sub(t.Verts[i])
			vertDists[i] = v[0]*v[0] + v[1]*v[1]
			sortedVertIs[i] = i
		}
		sorter := indexedFloatSorter{vertDists, sortedVertIs, t.VertI}
		sort.Sort(&sorter)

		for j := 0; j < sorter.l; j++ {
			n := sorter.is[j]
			for i := 0; i < t.TriangleI; i += 3 {
				if t.Triangles[i] != n && t.Triangles[i+1] != n && t.Triangles[i+2] != n {
					continue
				}
				if pointInTriangle(pt,
					t.Verts[t.Triangles[i]],
					t.Verts[t.Triangles[i+1]],
					t.Verts[t.Triangles[i+2]]) {
					found = true
					if parentTriIsIdx < 2 {
						parentTriIs[parentTriIsIdx] = i
						parentTriIsIdx++
					} else {
						duplicate = true
						break
					}
				}
			}
			if found {
				// the first vert that has any tris containing us will have all
				// the tris
				break
			}
		}
	}
	if !found {
		// todo: handle this and other panics as a returned error
		panic("point out-of-bounds")
	}
	if duplicate {
		// point is a duplicate
		for dupI := 0; dupI < t.VertI; dupI++ {
			if t.Verts[dupI].Sub(mgl32.Vec2{x, y}).Len() < 1e-6 {
				return dupI
			}
		}
		// this shouldn't be reached:
		return -1
	}
	ptI := t.VertI
	t.Verts[ptI] = pt
	t.VertI++
	// indices into the dirty set of t.Triangles:
	t.checkTriI = 0
	if parentTriIs[1] != -1 {
		// split 2 tris => 4 tris
		// find the clockwise quad points:
		quad := t.getSharedQuad(parentTriIs[0], parentTriIs[1])
		// sorted common edge:
		edge := [2]int{quad[1], quad[3]}
		if quad[1] > quad[3] {
			edge = [2]int{quad[3], quad[1]}
		}
		// find old edge
		oldEdgeI := -1
		for i := 0; i < t.EdgeI; i += 2 {
			if t.Edges[i] == edge[0] && t.Edges[i+1] == edge[1] {
				oldEdgeI = i
				break
			}
		}
		// generate new tris and edges
		// 0:
		t.Triangles[parentTriIs[0]] = quad[0]
		t.Triangles[parentTriIs[0]+1] = quad[1]
		t.Triangles[parentTriIs[0]+2] = ptI
		// 1:
		t.Triangles[parentTriIs[1]] = quad[1]
		t.Triangles[parentTriIs[1]+1] = quad[2]
		t.Triangles[parentTriIs[1]+2] = ptI
		// our edge sortedness is guaranteed here because ptI is the
		// largest index
		t.Edges[oldEdgeI] = quad[1]
		t.Edges[oldEdgeI+1] = ptI

		t.Triangles[t.TriangleI+0] = quad[2]
		t.Triangles[t.TriangleI+1] = quad[3]
		t.Triangles[t.TriangleI+2] = ptI
		t.Triangles[t.TriangleI+3] = quad[3]
		t.Triangles[t.TriangleI+4] = quad[0]
		t.Triangles[t.TriangleI+5] = ptI
		t.Edges[t.EdgeI+0] = quad[2]
		t.Edges[t.EdgeI+1] = ptI
		t.Edges[t.EdgeI+2] = quad[3]
		t.Edges[t.EdgeI+3] = ptI
		t.Edges[t.EdgeI+4] = quad[0]
		t.Edges[t.EdgeI+5] = ptI
		fixedI := t.EdgeI >> 1
		t.fixed[fixedI+0] = false
		t.fixed[fixedI+1] = false
		t.fixed[fixedI+2] = false
		t.checkTris[t.checkTriI+0] = parentTriIs[0]
		t.checkTris[t.checkTriI+1] = parentTriIs[1]
		t.checkTris[t.checkTriI+2] = t.TriangleI
		t.checkTris[t.checkTriI+3] = t.TriangleI + 3
		t.TriangleI += 6
		t.EdgeI += 6
		t.checkTriI += 4
	} else {
		// split 1 tri => 3 tris
		triI := parentTriIs[0]
		triVs := [3]int{
			t.Triangles[triI], t.Triangles[triI+1], t.Triangles[triI+2]}
		t.Triangles[triI] = triVs[0]
		t.Triangles[triI+1] = triVs[1]
		t.Triangles[triI+2] = ptI

		t.Triangles[t.TriangleI+0] = triVs[1]
		t.Triangles[t.TriangleI+1] = triVs[2]
		t.Triangles[t.TriangleI+2] = ptI
		t.Triangles[t.TriangleI+3] = triVs[2]
		t.Triangles[t.TriangleI+4] = triVs[0]
		t.Triangles[t.TriangleI+5] = ptI
		t.Edges[t.EdgeI+0] = triVs[1]
		t.Edges[t.EdgeI+1] = ptI
		t.Edges[t.EdgeI+2] = triVs[2]
		t.Edges[t.EdgeI+3] = ptI
		t.Edges[t.EdgeI+4] = triVs[0]
		t.Edges[t.EdgeI+5] = ptI
		fixedI := t.EdgeI >> 1
		t.fixed[fixedI+0] = false
		t.fixed[fixedI+1] = false
		t.fixed[fixedI+2] = false
		t.checkTris[t.checkTriI+0] = triI
		t.checkTris[t.checkTriI+1] = t.TriangleI
		t.checkTris[t.checkTriI+2] = t.TriangleI + 3
		t.TriangleI += 6
		t.EdgeI += 6
		t.checkTriI += 3
	}
	for t.checkTriI > 0 {
		triI := t.checkTris[0]
		triV := [3]int{
			t.Triangles[triI], t.Triangles[triI+1], t.Triangles[triI+2]}
		// for all edges
		for i := 0; i < 3; i++ {
			edge := [2]int{triV[(i+1)%3], triV[i]}
			sortedEdge := [2]int{edge[0], edge[1]}
			if edge[0] > edge[1] {
				sortedEdge = [2]int{edge[1], edge[0]}
			}
			// find common edge index:
			edgeI := 0
			for ; edgeI < t.EdgeI; edgeI += 2 {
				if t.Edges[edgeI] == sortedEdge[0] &&
					t.Edges[edgeI+1] == sortedEdge[1] {
					break
				}
			}
			// if the edge is locked, give up now (at this step, this only
			// applies to the initial boundary edges)
			if t.fixed[edgeI/2] {
				continue
			}
			// find the triangle on the other side
			found := false
			otherTriI := -1
			for n := 0; n < t.TriangleI; n += 3 {
				if n == triI {
					continue
				}
				if t.Triangles[n] == edge[0] && t.Triangles[n+1] == edge[1] {
					found = true
					otherTriI = n
					break
				}
				if t.Triangles[n+1] == edge[0] && t.Triangles[n+2] == edge[1] {
					found = true
					otherTriI = n
					break
				}
				if t.Triangles[n+2] == edge[0] && t.Triangles[n] == edge[1] {
					found = true
					otherTriI = n
					break
				}
			}
			// no neighbor?
			if !found {
				continue
			}
			// circumcircle test:
			quad := t.getSharedQuad(triI, otherTriI)
			a := t.Verts[quad[0]]
			b := t.Verts[quad[1]]
			c := t.Verts[quad[2]]
			d := t.Verts[quad[3]]
			sign := (mgl32.Mat4{
				d[0], d[1], d[0]*d[0] + d[1]*d[1], 1,
				c[0], c[1], c[0]*c[0] + c[1]*c[1], 1,
				b[0], b[1], b[0]*b[0] + b[1]*b[1], 1,
				a[0], a[1], a[0]*a[0] + a[1]*a[1], 1,
			}).Det()
			// if the determinant is too close to 0, we'll get stuck in a cycle
			if sign > 1e-7 {
				// flip: BD => AC
				t.Triangles[triI] = quad[0]
				t.Triangles[triI+1] = quad[1]
				t.Triangles[triI+2] = quad[2]
				t.Triangles[otherTriI] = quad[0]
				t.Triangles[otherTriI+1] = quad[2]
				t.Triangles[otherTriI+2] = quad[3]
				newEdge := [2]int{quad[0], quad[2]}
				if newEdge[0] > newEdge[1] {
					newEdge = [2]int{quad[2], quad[0]}
				}
				t.Edges[edgeI] = newEdge[0]
				t.Edges[edgeI+1] = newEdge[1]
				t.checkTris[t.checkTriI+0] = triI
				t.checkTris[t.checkTriI+1] = otherTriI
				t.checkTriI += 2
				break
			}
		}
		// remove the checked triangle:
		t.checkTris[0] = t.checkTris[t.checkTriI-1]
		t.checkTriI--
	}
	return ptI
}

// AddEdge forces an edge to exist in the triangulation.  This edge is then
// guaranteed to exist in the triangulation, unless a successive call to AddEdge
// specifies an edge that intersects this one.  If an intersecting edge is later
// specified, the later edge will "win".
func (t *Triangulation) AddEdge(indexA, indexB int) {
	if indexA == indexB {
		panic("bad graph")
	}
	edge := [2]int{indexA, indexB}
	if edge[0] > edge[1] {
		edge = [2]int{indexB, indexA}
	}
	edgeExists := false
	for i := 0; i < t.EdgeI; i += 2 {
		if t.Edges[i] == edge[0] && t.Edges[i+1] == edge[1] {
			edgeExists = true
			t.fixed[i/2] = true
			break
		}
	}
	if edgeExists {
		return
	}
	crossedTri := [3]int{}
	crossedTriI := -1
	for i := 0; i < t.TriangleI; i += 3 {
		if t.Triangles[i] == edge[0] {
			if pointInAngle(t.Verts[edge[1]],
				t.Verts[t.Triangles[i]],
				t.Verts[t.Triangles[i+1]],
				t.Verts[t.Triangles[i+2]]) {
				crossedTri = [3]int{t.Triangles[i],
					t.Triangles[i+1],
					t.Triangles[i+2]}
				crossedTriI = i
				break
			}
		}
		if t.Triangles[i+1] == edge[0] {
			if pointInAngle(t.Verts[edge[1]],
				t.Verts[t.Triangles[i+1]],
				t.Verts[t.Triangles[i+2]],
				t.Verts[t.Triangles[i]]) {
				crossedTri = [3]int{t.Triangles[i+1],
					t.Triangles[i+2],
					t.Triangles[i]}
				crossedTriI = i
				break
			}
		}
		if t.Triangles[i+2] == edge[0] {
			if pointInAngle(t.Verts[edge[1]],
				t.Verts[t.Triangles[i+2]],
				t.Verts[t.Triangles[i]],
				t.Verts[t.Triangles[i+1]]) {
				crossedTri = [3]int{t.Triangles[i+2],
					t.Triangles[i],
					t.Triangles[i+1]}
				crossedTriI = i
				break
			}
		}
	}
	ptA := t.Verts[edge[0]]
	ptB := t.Verts[edge[1]]
	ptsU := []int{crossedTri[1]}
	ptsL := []int{crossedTri[2]}
	deadTriIs := []int{crossedTriI}
	deadEdges := []int{}
	t.newTriI = 0
	t.newEdgeI = 0
	ringEdges := []int{crossedTri[0], crossedTri[1], crossedTri[0], crossedTri[2]}
	for {
		// get opposite triangle:
		otherTriI := -1
		otherVertI := -1
		for n := 0; n < t.TriangleI; n += 3 {
			if n == crossedTriI {
				continue
			}
			if t.Triangles[n] == crossedTri[2] && t.Triangles[n+1] == crossedTri[1] {
				otherTriI = n
				otherVertI = t.Triangles[n+2]
				break
			}
			if t.Triangles[n+1] == crossedTri[2] && t.Triangles[n+2] == crossedTri[1] {
				otherTriI = n
				otherVertI = t.Triangles[n]
				break
			}
			if t.Triangles[n+2] == crossedTri[2] && t.Triangles[n] == crossedTri[1] {
				otherTriI = n
				otherVertI = t.Triangles[n+1]
				break
			}
		}
		deadTriIs = append(deadTriIs, otherTriI)
		deadEdges = append(deadEdges, crossedTri[1], crossedTri[2])
		if len(deadTriIs) > 1e5 {
			// in this case, we've either managed to loop around a small set of
			// triangles (bad graph), or the edge is actually crossing 10k tris
			panic("probable infinite loop detected")
		}
		if otherVertI == edge[1] {
			ringEdges = append(ringEdges, crossedTri[1], otherVertI, crossedTri[2], otherVertI)
			break
		}
		oppositePt := t.Verts[otherVertI]
		ptSide := (ptB[0]-ptA[0])*(oppositePt[1]-ptA[1]) -
			(ptB[1]-ptA[1])*(oppositePt[0]-ptA[0])
		if ptSide > 1e-12 { // above
			ptsU = append(ptsU, otherVertI)
			ringEdges = append(ringEdges, crossedTri[1], otherVertI)
			crossedTri = [3]int{crossedTri[1], otherVertI, crossedTri[2]}
			crossedTriI = otherTriI
		} else if ptSide < -1e-12 { // below
			ptsL = append(ptsL, otherVertI)
			ringEdges = append(ringEdges, crossedTri[2], otherVertI)
			crossedTri = [3]int{crossedTri[2], crossedTri[1], otherVertI}
			crossedTriI = otherTriI
		} else { // incident
			t.AddEdge(otherVertI, edge[1])
			edge[1] = otherVertI
			break
		}
	}
	t.retriangulate(ringEdges, ptsU, edge)
	edge = [2]int{edge[1], edge[0]}
	t.retriangulate(ringEdges, ptsL, edge)
	if edge[0] > edge[1] {
		edge = [2]int{edge[1], edge[0]}
	}
	t.newEdgeI -= 2
	for i := 0; i < len(deadTriIs); i++ {
		t.Triangles[deadTriIs[i]] = t.newTris[3*i]
		t.Triangles[deadTriIs[i]+1] = t.newTris[3*i+1]
		t.Triangles[deadTriIs[i]+2] = t.newTris[3*i+2]
	}
	for i := 0; i < len(deadEdges); i += 2 {
		deadEdge := []int{deadEdges[i], deadEdges[i+1]}
		if deadEdges[i] > deadEdges[i+1] {
			deadEdge = []int{deadEdges[i+1], deadEdges[i]}
		}
		newEdge := [2]int{t.newEdges[i], t.newEdges[i+1]}
		if t.newEdges[i] > t.newEdges[i+1] {
			newEdge = [2]int{t.newEdges[i+1], t.newEdges[i]}
		}
		last := i == len(deadEdges)-2
		for j := 0; j < t.EdgeI; j += 2 {
			if deadEdge[0] == t.Edges[j] && deadEdge[1] == t.Edges[j+1] {
				t.Edges[j] = newEdge[0]
				t.Edges[j+1] = newEdge[1]
				if last {
					t.fixed[j/2] = true
				}
				break
			}
		}
	}
}

func (t *Triangulation) retriangulate(ringEdges, vertIs []int, edgeIs [2]int) {
	cI := -1
	if len(vertIs) > 1 {
		cI = vertIs[0]
		c := t.Verts[cI]
		// maintaining sanity about geometric orientation here is
		// a bit tricky
		a := t.Verts[edgeIs[0]]
		b := t.Verts[edgeIs[1]]
		// find the closest vert to the edge:
		for i := 1; i < len(vertIs); i++ {
			d := t.Verts[vertIs[i]]
			sign := (mgl32.Mat4{
				a[0], a[1], a[0]*a[0] + a[1]*a[1], 1,
				b[0], b[1], b[0]*b[0] + b[1]*b[1], 1,
				c[0], c[1], c[0]*c[0] + c[1]*c[1], 1,
				d[0], d[1], d[0]*d[0] + d[1]*d[1], 1,
			}).Det()
			if sign > 0 {
				cI = vertIs[i]
				c = t.Verts[cI]
			}
		}
		// partition vertIs into left/right of c:
		// left/right is determined by following the edge ring defined
		// by vertIs split on c
		leftVertIs := []int{}
		rightVertIs := []int{}
		{
			ringI := edgeIs[0]
			prevRingI := -1
			right := false
			ptInRing := func(idx int) (onRing bool, onEdge bool) {
				for _, vI := range vertIs {
					if vI == idx {
						return true, false
					}
				}
				if idx == edgeIs[1] || idx == edgeIs[0] {
					return true, true
				}
				return false, false
			}
			for {
				inRing := false
				shouldBreak := false
				testI := -1
				for i := 0; i < len(ringEdges); i += 2 {
					if ringEdges[i] == ringI {
						testI = ringEdges[i+1]
					}
					if ringEdges[i+1] == ringI {
						testI = ringEdges[i]
					}
					if testI >= 0 && prevRingI != testI {
						inRing, shouldBreak = ptInRing(testI)
						if shouldBreak {
							break
						}
						if inRing {
							prevRingI = ringI
							ringI = testI
							if ringI == cI {
								right = true
							} else if right {
								rightVertIs = append(rightVertIs, ringI)
							} else {
								leftVertIs = append(leftVertIs, ringI)
							}
							break
						}
					}
				}
				if shouldBreak {
					break
				}
				if ringI == prevRingI {
					panic("finding edge in ring won't terminate")
				}
			}
		}
		t.retriangulate(ringEdges, leftVertIs, [2]int{edgeIs[0], cI})
		t.retriangulate(ringEdges, rightVertIs, [2]int{cI, edgeIs[1]})
	}
	if len(vertIs) > 0 {
		if cI == -1 {
			cI = vertIs[0]
		}
		t.newTris[t.newTriI+0] = edgeIs[1]
		t.newTris[t.newTriI+1] = edgeIs[0]
		t.newTris[t.newTriI+2] = cI
		t.newEdges[t.newEdgeI+0] = edgeIs[0]
		t.newEdges[t.newEdgeI+1] = edgeIs[1]
		t.newTriI += 3
		t.newEdgeI += 2
	}
}

// getSharedQuad returns the quad a,b,c,d defined by triangles a,b,c and c,b,d;
// the returned quad array has the verts of the shared edge at quad[1] and
// quad[3].
func (t *Triangulation) getSharedQuad(triA, triB int) [4]int {
	quad := [4]int{}
	tris := [2]int{triA, triB}
	for n := 0; n < 2; n++ {
		triA = tris[n]
		triB = tris[1-n]
		for j := 0; j < 3; j++ {
			m := t.Triangles[triA+j]
			found := false
			for i := 0; i < 3; i++ {
				if t.Triangles[triB+i] == m {
					found = true
					break
				}
			}
			if !found {
				quad[n*2] = m
				quad[n*2+1] = t.Triangles[triA+((j+1)%3)]
				break
			}
		}
	}
	return quad
}

// getBarycentric returns the two barycentric components of the triangle abc for
// p relative to b and c
func getBarycentric(p, a, b, c mgl32.Vec2) (float32, float32) {
	v0 := c.Sub(a)
	v1 := b.Sub(a)
	v2 := p.Sub(a)
	dot00 := v0.Dot(v0)
	dot01 := v0.Dot(v1)
	dot02 := v0.Dot(v2)
	dot11 := v1.Dot(v1)
	dot12 := v1.Dot(v2)
	norm := (dot00*dot11 - dot01*dot01)
	u := (dot11*dot02 - dot01*dot12) / norm
	v := (dot00*dot12 - dot01*dot02) / norm
	return u, v
}

// pointInTriangle returns true if p is inside the triangle abc
func pointInTriangle(p, a, b, c mgl32.Vec2) bool {
	u, v := getBarycentric(p, a, b, c)
	// todo: math around rounding error better
	return u >= -4e-6 && v >= -4e-6 && u+v < 1.0000076
}

// pointInAngle returns true if p is inside the angle defined by lines ab and ac
func pointInAngle(p, a, b, c mgl32.Vec2) bool {
	u, v := getBarycentric(p, a, b, c)
	return u >= -4e-6 && v >= -4e-6
}

type indexedFloatSorter struct {
	fs []float32
	is []int
	l  int
}

func (s *indexedFloatSorter) Len() int {
	return s.l
}

func (s *indexedFloatSorter) Less(i, j int) bool {
	return s.fs[i] < s.fs[j]
}

func (s *indexedFloatSorter) Swap(i, j int) {
	idx := s.is[i]
	s.is[i] = s.is[j]
	s.is[j] = idx
	f := s.fs[i]
	s.fs[i] = s.fs[j]
	s.fs[j] = f
}
