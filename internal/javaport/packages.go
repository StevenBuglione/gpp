package javaport

import (
	"cmp"
	"maps"
	"slices"
)

// packageCycles finds Java package groups that must share an internal Go
// package or use an explicit boundary during lowering.
func packageCycles(files []SourceFile) [][]PackageIdentity {
	edges := map[PackageIdentity]map[PackageIdentity]bool{}
	for _, file := range files {
		if len(file.Declarations) > 0 {
			edges[sourcePackage(file)] = map[PackageIdentity]bool{}
		}
	}
	for _, file := range files {
		owner := sourcePackage(file)
		dependencies, exists := edges[owner]
		if !exists {
			continue
		}
		for _, reference := range file.TypeReferences {
			target := reference.PackageIdentity
			if _, internal := edges[target]; internal && owner != target {
				dependencies[target] = true
			}
		}
	}

	indices := map[PackageIdentity]int{}
	lowLinks := map[PackageIdentity]int{}
	onStack := map[PackageIdentity]bool{}
	var stack []PackageIdentity
	var cycles [][]PackageIdentity

	var visit func(PackageIdentity)
	visit = func(pkg PackageIdentity) {
		index := len(indices) + 1
		indices[pkg], lowLinks[pkg] = index, index
		stack = append(stack, pkg)
		onStack[pkg] = true

		for target := range edges[pkg] {
			if indices[target] == 0 {
				visit(target)
				lowLinks[pkg] = min(lowLinks[pkg], lowLinks[target])
			} else if onStack[target] {
				lowLinks[pkg] = min(lowLinks[pkg], indices[target])
			}
		}
		if lowLinks[pkg] != indices[pkg] {
			return
		}

		var component []PackageIdentity
		for {
			member := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[member] = false
			component = append(component, member)
			if member == pkg {
				break
			}
		}
		if len(component) > 1 {
			slices.SortFunc(component, comparePackage)
			cycles = append(cycles, component)
		}
	}

	for pkg := range maps.Keys(edges) {
		if indices[pkg] == 0 {
			visit(pkg)
		}
	}
	slices.SortFunc(cycles, func(left, right []PackageIdentity) int {
		return comparePackage(left[0], right[0])
	})
	return cycles
}

func sourcePackage(file SourceFile) PackageIdentity {
	return PackageIdentity{ModuleName: file.ModuleName, Package: file.Package}
}

func comparePackage(left, right PackageIdentity) int {
	if order := cmp.Compare(left.ModuleName, right.ModuleName); order != 0 {
		return order
	}
	return cmp.Compare(left.Package, right.Package)
}
