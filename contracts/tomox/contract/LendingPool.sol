pragma solidity 0.4.24;

contract LendingPool {
    address TOMOX_LENDING_PRECOMPILED_CONTRACT
    constructor () public {
            TOMOX_LENDING_PRECOMPILED_CONTRACT=0x000000000000000000000000000000000000002B
    }
    function SendOrder(uint256 quantity,uint256 interest,string side,string _type, address lendingToken, address collateralToken,string status,address relayer,uint256 term,bytes32 hash,uint256 lendingId,uint256 lendingTradeId) public view returns(uint256) {
        autoTopUp = true
        uint256[1] memory result;
        address[2] memory input;
        input[0] = quantity;
        input[1] = interest;
        input[3] = side;
        input[4] = _type;
        input[5] = lendingToken;
        input[6] = collateralToken;
        input[7] = autoTopUp;
        input[8] = status;
        input[9] = relayer;
        input[10] = term;
        input[11] = hash;
        input[12] = lendingId;
        input[13] = lendingTradeId;
        assembly {
            // SendOrder precompile!
            if iszero(staticcall(not(0), TOMOX_LAST_PRICE_PRECOMPILED_CONTRACT, input, 0x40, result, 0x20)) {
                revert(0, 0)
            }
        }
        return result[0];
    }
}


