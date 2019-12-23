// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package language

import (
	"fmt"
	"sort"
	"strings"

	"github.com/GoogleContainerTools/kpt-functions-sdk/go/pkg/swagger"
)

// TypeScript implements Language-specific logic for TypeScript.
type TypeScript struct {
	RefObjects map[swagger.Ref]swagger.Object
}

var _ Language = TypeScript{}

// File implements Language.
func (ts TypeScript) File(definition swagger.Definition) string {
	return fmt.Sprintf("%s.ts", definition.Meta().Package)
}

// PrintHeader implements Language.
func (ts TypeScript) PrintHeader(definitions []swagger.Definition) string {
	if len(definitions) == 0 {
		return ""
	}
	currentPackage := definitions[0].Meta().Package

	imports := getRefs(definitions)

	packagesMap := make(map[string]bool)
	for _, ref := range imports {
		if ref.Package == currentPackage {
			continue
		}
		packagesMap[ref.Package] = true
	}
	var packages []string
	for pkg := range packagesMap {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)

	var result []string

	// Import KubernetesObject only if there is a KubernetesObject in this package.
	hasKubernetesObject := false
	for _, def := range definitions {
		if swagger.IsKubernetesObject(ts.RefObjects, def.Meta().ToRef()) {
			hasKubernetesObject = true
		}
	}
	if hasKubernetesObject {
		result = append(result, "import { KubernetesObject } from '@googlecontainertools/kpt-functions';")
	}

	for _, pkg := range packages {
		alias := tsPackageAlias(pkg)
		result = append(result, fmt.Sprintf("import * as %s from './%s';", alias, pkg))
	}

	return strings.Join(result, "\n")
}

// Indent adds two spaces to the beginning of every line in the string.
func Indent(s string) string {
	splits := strings.Split(s, "\n")
	for i := range splits {
		if splits[i] != "" {
			splits[i] = "  " + splits[i]
		}
	}
	return strings.Join(splits, "\n")
}

func getRefs(definitions []swagger.Definition) []swagger.Ref {
	var refs []swagger.Ref
	for _, definition := range definitions {
		refs = append(refs, definition.Imports()...)
	}
	return refs
}

// PrintDefinition implements Language.
func (ts TypeScript) PrintDefinition(definition swagger.Definition) string {
	switch d := definition.(type) {
	case swagger.Object:
		return ts.typeScriptObject(d)
	case swagger.Alias:
		return typeScriptAlias(d)
	default:
		panic(fmt.Sprintf("unknown deinition type %T", d))
	}
}

func typeScriptAlias(a swagger.Alias) string {
	return fmt.Sprintf(`%sexport type %s = %s;`, printDescription(a.Description), a.Name, tsType(a.Package, a.Type))
}

func (ts TypeScript) typeScriptObject(o swagger.Object) string {
	var fields []string
	var constructors []string
	for _, property := range o.NamedProperties() {
		fields = append(fields, PrintTSTypesField(o.Package, property))

		if len(o.GroupVersionKinds) > 0 {
			switch property.Name {
			case "apiVersion":
				property.OverrideValue = fmt.Sprintf("%s.apiVersion", o.Name)
			case "kind":
				property.OverrideValue = fmt.Sprintf("%s.kind", o.Name)
			}

		}

		constructors = append(constructors, Indent(ts.PrintTSConstructorField(o.Package, property)))
	}

	descType := strings.Join(append(o.Namespace, o.Name), ".")
	if o.IsKubernetesObject {
		descType = descType + ".Interface"
	}
	constructor := ""
	isType := ""
	if gvk := o.GroupVersionKind(); gvk != nil {
		isType = fmt.Sprintf(`

export function is%s(o: any): o is %s {
  return o && o.apiVersion === %s.apiVersion && o.kind === %s.kind;
}`, o.Name, o.Name, o.Name, o.Name)
	}

	if o.HasRequiredFields() {
		optionalDesc := ""
		if !o.HasRequiredFields() {
			optionalDesc = "?"
		}
		constructor = fmt.Sprintf(`

constructor(desc%s: %s) {%s
}`, optionalDesc, descType, strings.Join(constructors, ""))
		constructor = Indent(constructor)
	}

	implements := ""
	if o.IsKubernetesObject {
		implements = " implements KubernetesObject"
	}

	return fmt.Sprintf(`%sexport class %s%s {
%s%s
}%s%s`, printDescription(o.Description), o.Name, implements, Indent(strings.Join(fields, "\n\n")), constructor, isType, ts.printNamespaceClasses(o))
}

// printDescription formats the description for TypeScript.
func printDescription(description string) string {
	// TODO: Pretty print descriptions.
	if description == "" {
		return ""
	}
	parts := strings.Split(description, "\n")
	for i, part := range parts {
		parts[i] = fmt.Sprintf(`// %s
`, part)
	}
	return strings.Join(parts, "")
}

func (ts TypeScript) printNamespaceClasses(o swagger.Object) string {

	if len(o.NestedTypes) == 0 && !o.IsKubernetesObject && len(o.GroupVersionKinds) == 0 {
		return ""
	}
	namespace := append(o.Namespace, o.Name)

	var classes []string
	if o.GroupVersionKind() != nil {
		classes = append(classes, Indent(printInterface(o)))
	}

	sort.Slice(o.NestedTypes, func(i, j int) bool {
		return o.NestedTypes[i].Name < o.NestedTypes[j].Name
	})
	for _, t := range o.NestedTypes {
		classes = append(classes, Indent(ts.typeScriptObject(t)))
	}

	constants := ""

	if len(o.GroupVersionKinds) > 0 {
		constants = Indent(fmt.Sprintf(`export const apiVersion = %q;
export const group = %q;
export const version = %q;
export const kind = %q;

`, o.GroupVersionKind().APIVersion(), o.GroupVersionKind().Group, o.GroupVersionKind().Version, o.GroupVersionKind().Kind))
	}

	namedFunc := ""
	if o.IsKubernetesObject {

		onlyMetaRequired := true
		for name, p := range o.Properties {
			if p.Required && name != "metadata" && name != "apiVersion" && name != "kind" {
				onlyMetaRequired = false
			}
		}
		if onlyMetaRequired {
			namedFunc = Indent(fmt.Sprintf(`// named constructs a %s with metadata.name set to name.
export function named(name: string): %s {
  return new %s({metadata: {name}});
}
`, o.Name, o.Name, o.Name))
		}
	}

	return fmt.Sprintf(`

export namespace %s {
%s%s%s
}`, namespace[len(namespace)-1], constants, namedFunc, strings.Join(classes, "\n"))
}

// printInterface prints the interface for KubernetesObjects.
func printInterface(o swagger.Object) string {
	var properties []string
	for _, property := range o.NamedProperties() {
		if o.GroupVersionKind() != nil {
			if property.Name == "apiVersion" || property.Name == "kind" {
				continue
			}
		}
		properties = append(properties, PrintTSInterfacesField(o.Package, property))
	}
	return fmt.Sprintf(`%sexport interface Interface {
%s
}`, printDescription(o.Description), Indent(strings.Join(properties, "\n\n")))
}

func tsPackageAlias(pkg string) string {
	splits := strings.Split(pkg, ".")
	splits = splits[len(splits)-3:]
	for i, split := range splits {
		if i == 0 {
			continue
		}
		splits[i] = strings.Title(split)
	}
	// Assumes packages have at least three elements. This assumption is not guaranteed to be true by OpenAPI, but is
	// unlikely to ever be false because of package naming conventions.
	return strings.Join(splits, "")
}

func tsType(currentPackage string, t swagger.Type) string {
	switch t2 := t.(type) {
	case swagger.Empty:
		return "object"
	case swagger.Primitive:
		return tsPrimitive(t2)
	case swagger.Ref:
		// TODO(b/141927141): Handle imported name collisions.
		//  As-is, a collision happens when the last three elements of package AND the Kind are the same for two
		//  different Definitions. This is exceedingly rare, and will cause circular references if it occurs.
		if t2.Package == currentPackage {
			return t2.Name
		}
		return fmt.Sprintf("%s.%s", tsPackageAlias(t2.Package), t2.Name)
	case swagger.Array:
		return fmt.Sprintf("%s[]", tsType(currentPackage, t2.Items))
	case swagger.Map:
		return fmt.Sprintf("{[key: string]: %s}", tsType(currentPackage, t2.Values))
	default:
		panic(fmt.Sprintf("unknown Type: %T", t2))
	}
}

func tsPrimitive(p swagger.Primitive) string {
	switch p.Type {
	case swagger.BOOLEAN:
		return "boolean"
	case swagger.INTEGER, swagger.NUMBER:
		return "number"
	case swagger.STRING:
		return "string"
	}

	panic(fmt.Sprintf("unknown Primitive %+v", p))
}

// PrintTSTypesField prints the property for the types.ts file for TypeScript.
func PrintTSTypesField(currentPackage string, property swagger.NamedProperty) string {
	optional := ""
	if !property.Required {
		optional = "?"
	}
	return fmt.Sprintf(`%spublic %s%s: %s;`, printDescription(property.Description), property.Name, optional, tsType(currentPackage, property.Type))
}

// PrintTSConstructorField prints the line in the constructor setting this field.
func (ts TypeScript) PrintTSConstructorField(currentPackage string, property swagger.NamedProperty) string {
	var value string
	if property.OverrideValue != "" {
		value = property.OverrideValue
	} else {
		value = ts.PrintTSConstructor(currentPackage, property.Type, "desc."+property.Name)
		if !property.Required {
			if array, isArray := property.Type.(swagger.Array); isArray {
				if ref, isRef := array.Items.(swagger.Ref); isRef {
					if swagger.IsKubernetesObject(ts.RefObjects, ref) {
						value = fmt.Sprintf("(desc.%s !== undefined) ? %s : undefined", property.Name, value)
					}
				}
			}
		}
	}
	return fmt.Sprintf(`
this.%s = %s;`, property.Name, value)
}

// PrintTSInterfacesField prints the property for the interfaces.ts file for TypeScript.
func PrintTSInterfacesField(currentPackage string, property swagger.NamedProperty) string {
	optional := ""
	if !property.Required {
		optional = "?"
	}
	return fmt.Sprintf(`%s%s%s: %s;`, printDescription(property.Description), property.Name, optional, tsType(currentPackage, property.Type))
}

func (ts TypeScript) PrintTSConstructor(currentPackage string, t swagger.Type, field string) string {
	switch t2 := t.(type) {
	case swagger.Empty, swagger.Primitive:
		return field
	case swagger.Ref:
		if swagger.IsKubernetesObject(ts.RefObjects, t2) {
			if t2.Package == currentPackage {
				return fmt.Sprintf("new %s(%s)", t2.Name, field)
			}
			return fmt.Sprintf("new %s.%s(%s)", tsPackageAlias(t2.Package), t2.Name, field)
		}
		return field
	case swagger.Array:
		if ref, isRef := t2.Items.(swagger.Ref); isRef {
			if swagger.IsKubernetesObject(ts.RefObjects, ref) {
				// TODO(b/141928661): Does not work on arrays of KubernetesObjects which contain arrays of KubernetesObjects.
				return fmt.Sprintf("%s.map((i) => %s)", field, ts.PrintTSConstructor(currentPackage, t2.Items, "i"))
			}
		}
		return field
	case swagger.Map:
		// TODO(b/141928662): Does not work when the values of the map are KubernetesObjects.
		return field
	default:
		panic(fmt.Sprintf("unkown type: %T", t2))
	}
}
