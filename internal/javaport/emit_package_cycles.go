package javaport

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
)

func (program *javaObjectProgram) emitPackageCycles(units map[string]EmittedPackage, cycles [][]PackageIdentity) error {
	for _, cycle := range cycles {
		members := make([]string, 0, len(cycle))
		for _, member := range cycle {
			members = append(members, member.Package)
		}
		// Use the member set to keep the implementation path stable when
		// unrelated packages are added or the input files are reordered.
		digest := sha256.Sum256([]byte(strings.Join(members, "\x00")))
		base := fmt.Sprintf("internal.gppjavaport.c%x", digest[:12])
		storage := base
		for suffix := 1; units[storage].Name != ""; suffix++ {
			storage = fmt.Sprintf("%s_%d", base, suffix)
		}
		for _, class := range program.layout {
			if index, found := slices.BinarySearch(members, class.file.Package); found {
				class.storagePackage = storage
				class.symbolPrefix = fmt.Sprintf("J%d_", index)
			}
		}
		for _, iface := range program.interfaceLayout {
			if index, found := slices.BinarySearch(members, iface.file.Package); found {
				iface.storagePackage = storage
				iface.symbolPrefix = fmt.Sprintf("J%d_", index)
			}
		}
		implementation, err := program.emitPackage(storage)
		if err != nil {
			return err
		}
		units[storage] = implementation.EmittedPackage
		for _, member := range members {
			facade, err := program.emitPackageFacade(member, storage)
			if err != nil {
				return err
			}
			units[member] = facade.EmittedPackage
		}
	}
	return nil
}

func (program javaObjectProgram) emitPackageFacade(name, storage string) (emittedObjectPackage, error) {
	program.pkg = name
	program.facadeOf = storage
	program.imports = map[string]string{}
	var source strings.Builder
	for _, iface := range program.interfaceLayout {
		if iface.file.Package != name {
			continue
		}
		emitJavaPermits(&source, iface.declaration, javaPermitsAPI(iface.declaration.Name))
		publicName := iface.declaration.Name
		fmt.Fprintf(&source, "\ntype %s = %s\n", publicName,
			program.qualify(storage, iface.storageName(publicName)))
		for _, field := range iface.constants {
			if err := program.emitInterfaceConstant(&source, iface, field, storage); err != nil {
				return emittedObjectPackage{}, err
			}
		}
		for _, method := range iface.methods {
			if method.isDefault {
				if err := program.emitInterfaceDefault(&source, method, storage); err != nil {
					return emittedObjectPackage{}, err
				}
			}
		}
	}
	for _, class := range program.layout {
		if class.file.Package != name {
			continue
		}
		publicName := class.declaration.Name
		emitJavaPermits(&source, class.declaration, javaPermitsAPI(publicName))
		fmt.Fprintf(&source, "\ntype %s = %s\n", publicName,
			program.qualify(storage, class.storageName(publicName)))
		for _, field := range class.fields {
			if !javaPublicConstant(field) {
				continue
			}
			name := publicName + field.Name
			target := program.qualify(storage, class.constantStorageName(field))
			if field.Type == javaStringType {
				result := program.objectGoType(field.Type)
				fmt.Fprintf(&source, "\nfunc %s() %s { return %s() }\n", name, result, target)
			} else {
				fmt.Fprintf(&source, "\nconst %s = %s\n", name, target)
			}
		}
		for _, constructor := range class.constructors {
			if !constructor.public || class.isAbstract() {
				continue
			}
			parameters, arguments := program.objectParameterList(constructor.declaration.Parameters)
			name := constructor.allocatorName()
			target := program.qualify(storage, class.storageName(name))
			fmt.Fprintf(&source, "\nfunc %s(%s) *%s { return %s(%s) }\n",
				name, parameters, publicName, target, arguments)
			if constructor.hasErrorAdapter() {
				name = constructor.errorAdapterName()
				target = program.qualify(storage, class.storageName(name))
				fmt.Fprintf(&source, "\nfunc %s(%s) (*%s, error) { return %s(%s) }\n",
					name, parameters, publicName, target, arguments)
			}
		}
		if class.activeInitialization {
			guard := class.staticGuardName()
			fmt.Fprintf(&source, "\nfunc %s() { %s() }\n", guard,
				program.qualify(storage, class.storageName(guard)))
		}
		for _, declaration := range class.fields {
			if slices.Contains(declaration.Modifiers, "STATIC") && declaration.Constant == nil && javaStaticObservable(declaration) {
				program.emitStaticField(&source, javaObjectField{declaration, class}, storage)
			}
		}
		for _, method := range class.staticMethods {
			if javaStaticObservable(method.declaration) {
				if err := program.emitStaticMethod(&source, method, storage); err != nil {
					return emittedObjectPackage{}, err
				}
			}
		}
	}
	return program.finishPackage(&source)
}

func javaPublicConstant(field Occurrence) bool {
	return field.Constant != nil && slices.Contains(field.Modifiers, "STATIC") && slices.Contains(field.Modifiers, "PUBLIC")
}

func (class *javaObjectClass) constantStorageName(field Occurrence) string {
	return class.storageName(class.declaration.Name + field.Name)
}
