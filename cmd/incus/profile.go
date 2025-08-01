package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/termios"
)

type profileColumn struct {
	Name string
	Data func(api.Profile) string
}

type cmdProfile struct {
	global *cmdGlobal
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfile) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("profile")
	cmd.Short = i18n.G("Manage profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage profiles`))

	// Add
	profileAddCmd := cmdProfileAdd{global: c.global, profile: c}
	cmd.AddCommand(profileAddCmd.Command())

	// Assign
	profileAssignCmd := cmdProfileAssign{global: c.global, profile: c}
	cmd.AddCommand(profileAssignCmd.Command())

	// Copy
	profileCopyCmd := cmdProfileCopy{global: c.global, profile: c}
	cmd.AddCommand(profileCopyCmd.Command())

	// Create
	profileCreateCmd := cmdProfileCreate{global: c.global, profile: c}
	cmd.AddCommand(profileCreateCmd.Command())

	// Delete
	profileDeleteCmd := cmdProfileDelete{global: c.global, profile: c}
	cmd.AddCommand(profileDeleteCmd.Command())

	// Device
	profileDeviceCmd := cmdConfigDevice{global: c.global, profile: c}
	cmd.AddCommand(profileDeviceCmd.Command())

	// Edit
	profileEditCmd := cmdProfileEdit{global: c.global, profile: c}
	cmd.AddCommand(profileEditCmd.Command())

	// Get
	profileGetCmd := cmdProfileGet{global: c.global, profile: c}
	cmd.AddCommand(profileGetCmd.Command())

	// List
	profileListCmd := cmdProfileList{global: c.global, profile: c}
	cmd.AddCommand(profileListCmd.Command())

	// Remove
	profileRemoveCmd := cmdProfileRemove{global: c.global, profile: c}
	cmd.AddCommand(profileRemoveCmd.Command())

	// Rename
	profileRenameCmd := cmdProfileRename{global: c.global, profile: c}
	cmd.AddCommand(profileRenameCmd.Command())

	// Set
	profileSetCmd := cmdProfileSet{global: c.global, profile: c}
	cmd.AddCommand(profileSetCmd.Command())

	// Show
	profileShowCmd := cmdProfileShow{global: c.global, profile: c}
	cmd.AddCommand(profileShowCmd.Command())

	// Unset
	profileUnsetCmd := cmdProfileUnset{global: c.global, profile: c, profileSet: &profileSetCmd}
	cmd.AddCommand(profileUnsetCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdProfileAdd struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<instance> <profile>"))
	cmd.Short = i18n.G("Add profiles to instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add profiles to instances`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProfiles(args[0], false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileAdd) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing instance name"))
	}

	// Add the profile
	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	inst.Profiles = append(inst.Profiles, args[1])

	op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s added to %s")+"\n", args[1], resource.name)
	}

	return nil
}

// Assign.
type cmdProfileAssign struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileAssign) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("assign", i18n.G("[<remote>:]<instance> <profiles>"))
	cmd.Aliases = []string{"apply"}
	cmd.Short = i18n.G("Assign sets of profiles to instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Assign sets of profiles to instances`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus profile assign foo default,bar
    Set the profiles for "foo" to "default" and "bar".

incus profile assign foo default
    Reset "foo" to only using the "default" profile.

incus profile assign foo ''
    Remove all profile from "foo"`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return c.global.cmpProfiles(args[0], false)
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileAssign) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Assign the profiles
	if resource.name == "" {
		return errors.New(i18n.G("Missing instance name"))
	}

	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	if args[1] != "" {
		inst.Profiles = strings.Split(args[1], ",")
	} else {
		inst.Profiles = nil
	}

	op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if args[1] == "" {
		args[1] = i18n.G("(none)")
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profiles %s applied to %s")+"\n", args[1], resource.name)
	}

	return nil
}

// Copy.
type cmdProfileCopy struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagTargetProject string
	flagRefresh       bool
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("copy", i18n.G("[<remote>:]<profile> [<remote>:]<profile>"))
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy profiles`))
	cmd.Flags().StringVar(&c.flagTargetProject, "target-project", "", i18n.G("Copy to a project different from the source")+"``")
	cmd.Flags().BoolVar(&c.flagRefresh, "refresh", false, i18n.G("Update the target profile from the source if it already exists"))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		if len(args) == 1 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileCopy) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args...)
	if err != nil {
		return err
	}

	source := resources[0]
	dest := resources[1]

	if source.name == "" {
		return errors.New(i18n.G("Missing source profile name"))
	}

	if dest.name == "" {
		dest.name = source.name
	}

	// Copy the profile
	profile, _, err := source.server.GetProfile(source.name)
	if err != nil {
		return err
	}

	if c.flagTargetProject != "" {
		dest.server = dest.server.UseProject(c.flagTargetProject)
	}

	// Refresh the profile if requested.
	if c.flagRefresh {
		err := dest.server.UpdateProfile(dest.name, profile.Writable(), "")
		if err == nil || !api.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}
	}

	newProfile := api.ProfilesPost{
		ProfilePut: profile.Writable(),
		Name:       dest.name,
	}

	return dest.server.CreateProfile(newProfile)
}

// Create.
type cmdProfileCreate struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagDescription string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Create profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create profiles`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus profile create p1
    Create a profile named p1

incus profile create p1 < config.yaml
    Create a profile named p1 with configuration from config.yaml`))

	cmd.RunE = c.Run

	cmd.Flags().StringVar(&c.flagDescription, "description", "", i18n.G("Profile description")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileCreate) Run(cmd *cobra.Command, args []string) error {
	var stdinData api.ProfilePut

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// Create the profile
	profile := api.ProfilesPost{}
	profile.Name = resource.name
	profile.ProfilePut = stdinData

	if c.flagDescription != "" {
		profile.Description = c.flagDescription
	}

	err = resource.server.CreateProfile(profile)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s created")+"\n", resource.name)
	}

	return nil
}

// Delete.
type cmdProfileDelete struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<profile>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete profiles`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// Delete the profile
	err = resource.server.DeleteProfile(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdProfileEdit struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Edit profile configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit profile configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus profile edit <profile> < profile.yaml
    Update a profile using the content of profile.yaml`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdProfileEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the profile.
### Any line starting with a '# will be ignored.
###
### A profile consists of a set of configuration items followed by a set of
### devices.
###
### An example would look like:
### name: onenic
### config:
###   raw.lxc: lxc.aa_profile=unconfined
### devices:
###   eth0:
###     nictype: bridged
###     parent: mybr0
###     type: nic
###
### Note that the name is shown but cannot be changed`)
}

// Run runs the actual command logic.
func (c *cmdProfileEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ProfilePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateProfile(resource.name, newdata, "")
	}

	// Extract the current value
	profile, etag, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ProfilePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateProfile(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = textEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Get.
type cmdProfileGet struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagIsProperty bool
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<profile> <key>"))
	cmd.Short = i18n.G("Get values for profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for profile configuration keys`))

	cmd.RunE = c.Run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		if len(args) == 1 {
			return c.global.cmpProfileConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileGet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// Get the configuration key
	profile, _, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := profile.Writable()
		res, err := getFieldByJSONTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the profile %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", profile.Config[args[1]])
	}

	return nil
}

// List.
type cmdProfileList struct {
	global          *cmdGlobal
	profile         *cmdProfile
	flagFormat      string
	flagColumns     string
	flagAllProjects bool
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:] [<filter>...]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List profiles

Filters may be of the <key>=<value> form for property based filtering,
or part of the profile name. Filters must be delimited by a ','.

Examples:
  - "foo" lists all profiles that start with the name foo
  - "name=foo" lists all profiles that exactly have the name foo
  - "description=.*bar.*" lists all profiles with a description that contains "bar"

The -c option takes a (optionally comma-separated) list of arguments
that control which image attributes to output when displaying in table
or csv format.

Default column layout is: ndu

Column shorthand chars:
n - Profile Name
d - Description
u - Used By`))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagColumns, "columns", "c", defaultProfileColumns, i18n.G("Columns")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display profiles from all projects"))

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const (
	defaultProfileColumns            = "ndu"
	defaultProfileColumnsAllProjects = "endu"
)

func (c *cmdProfileList) parseColumns() ([]profileColumn, error) {
	columnsShorthandMap := map[rune]profileColumn{
		'n': {i18n.G("NAME"), c.profileNameColumnData},
		'e': {i18n.G("PROJECT"), c.projectNameColumnData},
		'd': {i18n.G("DESCRIPTION"), c.descriptionColumnData},
		'u': {i18n.G("USED BY"), c.usedByColumnData},
	}

	// Add project column if --all-projects flag specified and no custom column was passed.
	if c.flagAllProjects {
		if c.flagColumns == defaultProfileColumns {
			c.flagColumns = defaultProfileColumnsAllProjects
		}
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []profileColumn{}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

func (c *cmdProfileList) profileNameColumnData(profile api.Profile) string {
	return profile.Name
}

func (c *cmdProfileList) descriptionColumnData(profile api.Profile) string {
	return profile.Description
}

func (c *cmdProfileList) projectNameColumnData(profile api.Profile) string {
	return profile.Project
}

func (c *cmdProfileList) usedByColumnData(profile api.Profile) string {
	return fmt.Sprintf("%d", len(profile.UsedBy))
}

// Run runs the actual command logic.
func (c *cmdProfileList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, -1)
	if exit {
		return err
	}

	if c.global.flagProject != "" && c.flagAllProjects {
		return errors.New(i18n.G("Can't specify --project with --all-projects"))
	}

	// Parse remote and filters.
	remote := ""
	filters := []string{}

	if len(args) != 0 {
		filters = args
		if strings.Contains(args[0], ":") && !strings.Contains(args[0], "=") {
			remote = args[0]
			filters = args[1:]
		}
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	flattenedFilters := []string{}
	for _, filter := range filters {
		flattenedFilters = append(flattenedFilters, strings.Split(filter, ",")...)
	}

	filters = flattenedFilters

	if len(filters) > 0 && !strings.Contains(filters[0], "=") {
		filters[0] = fmt.Sprintf("name=^%s($|.*)", regexp.QuoteMeta(filters[0]))
	}

	serverFilters, _ := getServerSupportedFilters(filters, []string{}, false)

	// List profiles
	var profiles []api.Profile
	if c.flagAllProjects {
		profiles, err = resource.server.GetProfilesAllProjectsWithFilter(serverFilters)
	} else {
		profiles, err = resource.server.GetProfilesWithFilter(serverFilters)
	}

	if err != nil {
		return err
	}

	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, profile := range profiles {
		line := []string{}
		for _, column := range columns {
			line = append(line, column.Data(profile))
		}

		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, profiles)
}

// Remove.
type cmdProfileRemove struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<instance> <profile>"))
	cmd.Short = i18n.G("Remove profiles from instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove profiles from instances`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpProfiles(args[0], false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileRemove) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing instance name"))
	}

	// Remove the profile
	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	if !slices.Contains(inst.Profiles, args[1]) {
		return fmt.Errorf(i18n.G("Profile %s isn't currently applied to %s"), args[1], resource.name)
	}

	profiles := []string{}
	for _, profile := range inst.Profiles {
		if profile == args[1] {
			continue
		}

		profiles = append(profiles, profile)
	}

	inst.Profiles = profiles

	op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s removed from %s")+"\n", args[1], resource.name)
	}

	return nil
}

// Rename.
type cmdProfileRename struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<profile> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename profiles")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename profiles`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileRename) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// Rename the profile
	err = resource.server.RenameProfile(resource.name, api.ProfilePost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Profile %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Set.
type cmdProfileSet struct {
	global  *cmdGlobal
	profile *cmdProfile

	flagIsProperty bool
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<profile> <key>=<value>..."))
	cmd.Short = i18n.G("Set profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set profile configuration keys

For backward compatibility, a single configuration key may still be set with:
    incus profile set [<remote>:]<profile> <key> <value>`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		if len(args) == 1 {
			return c.global.cmpInstanceAllKeys()
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileSet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// Get the profile
	profile, etag, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	// Set the configuration key
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := profile.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJSONTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		maps.Copy(writable.Config, keys)
	}

	return resource.server.UpdateProfile(resource.name, writable, etag)
}

// Show.
type cmdProfileShow struct {
	global  *cmdGlobal
	profile *cmdProfile
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<profile>"))
	cmd.Short = i18n.G("Show profile configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show profile configurations`))

	cmd.RunE = c.Run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing profile name"))
	}

	// Show the profile
	profile, _, err := resource.server.GetProfile(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&profile)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdProfileUnset struct {
	global     *cmdGlobal
	profile    *cmdProfile
	profileSet *cmdProfileSet

	flagIsProperty bool
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdProfileUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<profile> <key>"))
	cmd.Short = i18n.G("Unset profile configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset profile configuration keys`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a profile property"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpProfiles(toComplete, true)
		}

		if len(args) == 1 {
			return c.global.cmpProfileConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdProfileUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.profileSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.profileSet.Run(cmd, args)
}
