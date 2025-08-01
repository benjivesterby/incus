package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cowsql/go-cowsql/client"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	incus "github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/node"
	"github.com/lxc/incus/v6/internal/server/sys"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/termios"
)

type cmdAdmin struct {
	global *cmdGlobal
}

func (c *cmdAdmin) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Hidden = true
	cmd.Use = "admin"

	// Cluster
	clusterCmd := cmdCluster{global: c.global}
	cmd.AddCommand(clusterCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdCluster struct {
	global *cmdGlobal
}

// Command returns a cobra command for inclusion.
func (c *cmdCluster) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "cluster"
	cmd.Short = "Low-level cluster administration commands"
	cmd.Long = `Description:
  Low level administration tools for inspecting and recovering clusters.
`
	// List database nodes
	listDatabase := cmdClusterListDatabase{global: c.global}
	cmd.AddCommand(listDatabase.command())

	// Recover
	recoverFromQuorumLoss := cmdClusterRecoverFromQuorumLoss{global: c.global}
	cmd.AddCommand(recoverFromQuorumLoss.command())

	// Remove a raft node.
	removeRaftNode := cmdClusterRemoveRaftNode{global: c.global}
	cmd.AddCommand(removeRaftNode.command())

	// Edit cluster configuration.
	clusterEdit := cmdClusterEdit{global: c.global}
	cmd.AddCommand(clusterEdit.command())

	// Show cluster configuration.
	clusterShow := cmdClusterShow{global: c.global}
	cmd.AddCommand(clusterShow.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

const SegmentComment = "# Latest dqlite segment ID: %s"

// ClusterMember is a more human-readable representation of the db.RaftNode struct.
type ClusterMember struct {
	ID      uint64 `yaml:"id"`
	Name    string `yaml:"name,omitempty"`
	Address string `yaml:"address"`
	Role    string `yaml:"role"`
}

// ClusterConfig is a representation of the current cluster configuration.
type ClusterConfig struct {
	Members []ClusterMember `yaml:"members"`
}

// ToRaftNode converts a ClusterConfig struct to a RaftNode struct.
func (c ClusterMember) ToRaftNode() (*db.RaftNode, error) {
	node := &db.RaftNode{
		NodeInfo: client.NodeInfo{
			ID:      c.ID,
			Address: c.Address,
		},
		Name: c.Name,
	}

	var role db.RaftRole
	switch c.Role {
	case "voter":
		role = db.RaftVoter
	case "stand-by":
		role = db.RaftStandBy
	case "spare":
		role = db.RaftSpare
	default:
		return nil, fmt.Errorf("unknown raft role: %q", c.Role)
	}

	node.Role = role

	return node, nil
}

type cmdClusterEdit struct {
	global *cmdGlobal
}

func (c *cmdClusterEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "edit"
	cmd.Short = "Edit cluster configuration as YAML"
	cmd.Long = `Description:
	Edit cluster configuration as YAML.`
	cmd.RunE = c.run

	return cmd
}

func (c *cmdClusterEdit) run(_ *cobra.Command, _ []string) error {
	// Make sure that the daemon is not running.
	_, err := incus.ConnectIncusUnix("", nil)
	if err == nil {
		return errors.New("The daemon is running, please stop it first.")
	}

	database, err := db.OpenNode(filepath.Join(sys.DefaultOS().VarDir, "database"), nil)
	if err != nil {
		return err
	}

	var nodes []db.RaftNode
	err = database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		clusterAddress := config.ClusterAddress()
		if clusterAddress == "" {
			return errors.New(`Can't edit cluster configuration as server isn't clustered (missing "cluster.https_address" config)`)
		}

		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return err
	}

	segmentID, err := db.DqliteLatestSegment()
	if err != nil {
		return err
	}

	config := ClusterConfig{Members: []ClusterMember{}}

	for _, node := range nodes {
		member := ClusterMember{ID: node.ID, Name: node.Name, Address: node.Address, Role: node.Role.String()}
		config.Members = append(config.Members, member)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	var content []byte
	if !termios.IsTerminal(unix.Stdin) {
		content, err = io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
	} else {
		if len(config.Members) > 0 {
			data = []byte(fmt.Sprintf(SegmentComment, segmentID) + "\n\n" + string(data))
		}

		content, err = textEditor("", data)
		if err != nil {
			return err
		}
	}

	for {
		newConfig := ClusterConfig{}
		err = yaml.Unmarshal(content, &newConfig)
		if err == nil {
			// Convert ClusterConfig back to RaftNodes.
			newNodes := []db.RaftNode{}
			var newNode *db.RaftNode
			for _, node := range newConfig.Members {
				newNode, err = node.ToRaftNode()
				if err != nil {
					break
				}

				newNodes = append(newNodes, *newNode)
			}

			// Ensure new configuration is valid.
			if err == nil {
				err = validateNewConfig(nodes, newNodes)
				if err == nil {
					err = cluster.Reconfigure(database, newNodes)
				}
			}
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Config validation error: %s\n", err)
			fmt.Println("Press enter to open the editor again or ctrl+c to abort change")
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

func validateNewConfig(oldNodes []db.RaftNode, newNodes []db.RaftNode) error {
	if len(oldNodes) > len(newNodes) {
		return errors.New("Removing cluster members is not supported")
	}

	if len(oldNodes) < len(newNodes) {
		return errors.New("Adding cluster members is not supported")
	}

	numNewVoters := 0
	for i, newNode := range newNodes {
		oldNode := oldNodes[i]

		// IDs should not be reordered among cluster members.
		if oldNode.ID != newNode.ID {
			return errors.New("Changing cluster member ID is not supported")
		}

		// If the name field could not be populated, just ignore the new value.
		if oldNode.Name != "" && newNode.Name != "" && oldNode.Name != newNode.Name {
			return errors.New("Changing cluster member name is not supported")
		}

		if oldNode.Role == db.RaftSpare && newNode.Role == db.RaftVoter {
			return fmt.Errorf("A %q cluster member cannot become a %q", db.RaftSpare.String(), db.RaftVoter.String())
		}

		if newNode.Role == db.RaftVoter {
			numNewVoters++
		}
	}

	if numNewVoters < 2 && len(newNodes) > 2 {
		return fmt.Errorf("Number of %q must be 2 or more", db.RaftVoter.String())
	} else if numNewVoters < 1 {
		return fmt.Errorf("At least one member must be a %q", db.RaftVoter.String())
	}

	return nil
}

type cmdClusterShow struct {
	global *cmdGlobal
}

func (c *cmdClusterShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "show"
	cmd.Short = "Show cluster configuration as YAML"
	cmd.Long = `Description:
	Show cluster configuration as YAML.`
	cmd.RunE = c.run

	return cmd
}

func (c *cmdClusterShow) run(_ *cobra.Command, _ []string) error {
	database, err := db.OpenNode(filepath.Join(sys.DefaultOS().VarDir, "database"), nil)
	if err != nil {
		return err
	}

	var nodes []db.RaftNode
	err = database.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return err
	}

	segmentID, err := db.DqliteLatestSegment()
	if err != nil {
		return err
	}

	config := ClusterConfig{Members: []ClusterMember{}}

	for _, node := range nodes {
		member := ClusterMember{ID: node.ID, Name: node.Name, Address: node.Address, Role: node.Role.String()}
		config.Members = append(config.Members, member)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	if len(config.Members) > 0 {
		fmt.Printf(SegmentComment+"\n\n%s", segmentID, data)
	} else {
		fmt.Print(data)
	}

	return nil
}

type cmdClusterListDatabase struct {
	global *cmdGlobal

	flagFormat string
}

func (c *cmdClusterListDatabase) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "list-database"
	cmd.Aliases = []string{"ls"}
	cmd.Short = "Print the addresses of the cluster members serving the database"
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", `Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.run

	return cmd
}

func (c *cmdClusterListDatabase) run(_ *cobra.Command, _ []string) error {
	defaultOS := sys.DefaultOS()

	dbconn, err := db.OpenNode(filepath.Join(defaultOS.VarDir, "database"), nil)
	if err != nil {
		return fmt.Errorf("Failed to open local database: %w", err)
	}

	addresses, err := cluster.ListDatabaseNodes(dbconn)
	if err != nil {
		return fmt.Errorf("Failed to get database nodes: %w", err)
	}

	columns := []string{"Address"}
	data := make([][]string, len(addresses))
	for i, address := range addresses {
		data[i] = []string{address}
	}

	_ = cli.RenderTable(os.Stdout, c.flagFormat, columns, data, nil)

	return nil
}

type cmdClusterRecoverFromQuorumLoss struct {
	global             *cmdGlobal
	flagNonInteractive bool
}

func (c *cmdClusterRecoverFromQuorumLoss) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "recover-from-quorum-loss"
	cmd.Short = "Recover an instance whose cluster has lost quorum"

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagNonInteractive, "quiet", "q", false, "Don't require user confirmation")

	return cmd
}

func (c *cmdClusterRecoverFromQuorumLoss) run(_ *cobra.Command, _ []string) error {
	// Make sure that the daemon is not running.
	_, err := incus.ConnectIncusUnix("", nil)
	if err == nil {
		return errors.New("The daemon is running, please stop it first.")
	}

	// Prompt for confirmation unless --quiet was passed.
	if !c.flagNonInteractive {
		err := c.promptConfirmation()
		if err != nil {
			return err
		}
	}

	os := sys.DefaultOS()

	db, err := db.OpenNode(filepath.Join(os.VarDir, "database"), nil)
	if err != nil {
		return fmt.Errorf("Failed to open local database: %w", err)
	}

	return cluster.Recover(db)
}

func (c *cmdClusterRecoverFromQuorumLoss) promptConfirmation() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(`You should run this command only if you are *absolutely* certain that this is
the only database node left in your cluster AND that other database nodes will
never come back (i.e. their daemon won't ever be started again).

This will make this server the only member of the cluster, and it won't
be possible to perform operations on former cluster members anymore.

However all information about former cluster members will be preserved in the
database, so you can possibly inspect it for further recovery.

You'll be able to permanently delete from the database all information about
former cluster members by running "incus cluster remove <member-name> --force".

See https://linuxcontainers.org/incus/docs/main/howto/cluster_recover/#recover-from-quorum-loss for more info.

Do you want to proceed? (yes/no): `)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{"yes"}, strings.ToLower(input)) {
		return errors.New("Recover operation aborted")
	}

	return nil
}

type cmdClusterRemoveRaftNode struct {
	global             *cmdGlobal
	flagNonInteractive bool
}

func (c *cmdClusterRemoveRaftNode) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "remove-raft-node <address>"
	cmd.Short = "Remove a raft node from the raft configuration"

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagNonInteractive, "quiet", "q", false, "Don't require user confirmation")

	return cmd
}

func (c *cmdClusterRemoveRaftNode) run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		_ = cmd.Help()
		return errors.New("Missing required arguments")
	}

	address := internalUtil.CanonicalNetworkAddress(args[0], ports.HTTPSDefaultPort)

	// Prompt for confirmation unless --quiet was passed.
	if !c.flagNonInteractive {
		err := c.promptConfirmation()
		if err != nil {
			return err
		}
	}

	client, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return fmt.Errorf("Failed to connect to daemon: %w", err)
	}

	endpoint := fmt.Sprintf("/internal/cluster/raft-node/%s", address)
	_, _, err = client.RawQuery("DELETE", endpoint, nil, "")
	if err != nil {
		return err
	}

	return nil
}

func (c *cmdClusterRemoveRaftNode) promptConfirmation() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(`You should run this command only if you ended up in an
inconsistent state where a node has been uncleanly removed (i.e. it doesn't show
up in "incus cluster list" but it's still in the raft configuration).

Do you want to proceed? (yes/no): `)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{"yes"}, strings.ToLower(input)) {
		return errors.New("Remove raft node operation aborted")
	}

	return nil
}

// Spawn the editor with a temporary YAML file for editing configs.
func textEditor(inPath string, inContent []byte) ([]byte, error) {
	var f *os.File
	var err error
	var path string

	// Detect the text editor to use
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
		if editor == "" {
			for _, p := range []string{"editor", "vi", "emacs", "nano"} {
				_, err := exec.LookPath(p)
				if err == nil {
					editor = p
					break
				}
			}
			if editor == "" {
				return []byte{}, errors.New("No text editor found, please set the EDITOR environment variable")
			}
		}
	}

	if inPath == "" {
		// If provided input, create a new file
		f, err = os.CreateTemp("", "incus_editor_")
		if err != nil {
			return []byte{}, err
		}

		reverter := revert.New()
		defer reverter.Fail()
		reverter.Add(func() {
			_ = f.Close()
			_ = os.Remove(f.Name())
		})

		err = os.Chmod(f.Name(), 0o600)
		if err != nil {
			return []byte{}, err
		}

		_, err = f.Write(inContent)
		if err != nil {
			return []byte{}, err
		}

		err = f.Close()
		if err != nil {
			return []byte{}, err
		}

		path = fmt.Sprintf("%s.yaml", f.Name())
		err = os.Rename(f.Name(), path)
		if err != nil {
			return []byte{}, err
		}

		reverter.Success()
		reverter.Add(func() { _ = os.Remove(path) })
	} else {
		path = inPath
	}

	cmdParts := strings.Fields(editor)
	cmd := exec.Command(cmdParts[0], append(cmdParts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return []byte{}, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return []byte{}, err
	}

	return content, nil
}
