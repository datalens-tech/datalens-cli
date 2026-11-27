package cli

import "github.com/spf13/cobra"

const cow = ` ______________________
/    ⠚⠛⠿⣷⣦⣄            \
| ⣀⣤⣤⣄⣀⣀⠈⠻⣿⣆           |
| ⣿⣿⣿⣿⣿⣿⣿⡆⣿⣿  DataLens |
| ⠙⠛⠛⠛⠋⠉⠁⣰⣿⠟           |
\    ⢤⣤⣶⣿⠟⠋            /
 ----------------------
        \   ^__^
         \  (oo)\_______
            (__)\       )\/\
                ||----w |
                ||     ||
`

func (a *app) cowCommand() *cobra.Command {
	return &cobra.Command{
		Use: "cow", Hidden: true, Args: exactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.writer.Result(map[string]string{"cow": cow}, cow)
		},
	}
}
