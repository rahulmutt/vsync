package main


func main() {
	if err := rootCmd().Execute(); err != nil {
		exitFn(1)
	}
}
