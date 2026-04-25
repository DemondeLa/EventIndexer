# EventIndexer/Makefile

# === 配置 ===
SOLC := solc
ABIGEN := abigen
CONTRACT := WinnerTakesAll
PKG := winner
OUTPUT_DIR := abigen/$(PKG)
BUILD_DIR := build

# === 目标 ===
.PHONY: winner clean

winner: $(OUTPUT_DIR)/$(CONTRACT).go

$(OUTPUT_DIR)/$(CONTRACT).go: contracts/$(CONTRACT).sol
	mkdir -p $(BUILD_DIR)
	$(SOLC) --bin --abi -o $(BUILD_DIR) contracts/$(CONTRACT).sol --overwrite
	abigen --bin=$(BUILD_DIR)/$(CONTRACT).bin --abi=$(BUILD_DIR)/$(CONTRACT).abi --pkg=$(PKG) --type=$(CONTRACT) --out=$(OUTPUT_DIR)/$(CONTRACT).go
	@echo " ✅ Generated $(OUTPUT_DIR)/$(CONTRACT).go"

clean:
	rm -rf $(BUILD_DIR)
	rm -f $(OUTPUT_DIR)/*.go