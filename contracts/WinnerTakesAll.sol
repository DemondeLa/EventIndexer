// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// 社区习惯：solidity layout order:
// struct/enum → state variables → events → errors → modifiers → constructor → functions
// function 名字(参数) [可见性] [状态可变性] [virtual/override] [自定义 modifier] { ... }

// 赢家通吃
contract WinnerTakesAll {

    struct Project {
        address submitter;
        string name;
        string url;
        uint256 totalRaised;
    }

    mapping(uint256 => Project) public projects;
    uint256 public projectCount; // 既是总数，也是下一个id
    uint256 immutable public deadlineSubmit; // 提交截止（Unix 秒）
    uint256 immutable public deadlineVote; // 投票截止（Unix 秒）
    bool private settled; // 是否已结算
    uint256 public winningProject; // 赢家项目 id（结算后赋值）

    event ProjectSubmitted(
        uint256 indexed projectId,
        address indexed submitter,
        string name,
        string url
    );

    event ProjectVoted(
        address indexed voter,
        uint256 indexed projectId,
        uint256 amount
    );

    event WinnerAnnounced(
        uint256 indexed projectId,
        address indexed winner,
        uint256 prize
    );

    event RoundClosedWithNoWinner();

    error InvalidProjectId(uint256 providedId);
//    error InvalidDeadline();
    error SubmitDeadlineTooEarly(uint256 provided, uint256 now);
    error VoteDeadlineBeforeSubmit(uint256 submit, uint256 vote);
    error SubmitPhaseEnded();
    error VotePhaseNotActive();
    error CampaignNotEnded();
    error InvalidAmount();
    error AlreadySettled();
    error PayoutFailed();

    modifier onlySubmitPhase() {
        if (block.timestamp >= deadlineSubmit)
            revert SubmitPhaseEnded();
        _;
    }

    modifier onlyVotePhase() {
        if (block.timestamp < deadlineSubmit || block.timestamp >= deadlineVote)
            revert VotePhaseNotActive();
        _;
    }

    modifier onlyAfterCampaign() {
        if (block.timestamp < deadlineVote)
            revert CampaignNotEnded();
        _;
    }

    constructor(uint256 _deadlineSubmit, uint256 _deadlineVote) {
        if (_deadlineSubmit <= block.timestamp)
            revert SubmitDeadlineTooEarly(_deadlineSubmit,block.timestamp);
        if (_deadlineVote <= _deadlineSubmit)
            revert VoteDeadlineBeforeSubmit(_deadlineSubmit, _deadlineVote);

        deadlineSubmit = _deadlineSubmit;
        deadlineVote = _deadlineVote;
    }

    /// @notice 提交项目
    function submitProject(string calldata name, string calldata url) external onlySubmitPhase {
        uint256 projectId = projectCount;
        projects[projectCount] = Project({
            submitter: msg.sender,
            name: name,
            url: url,
            totalRaised: 0
        });
        projectCount++;
        emit ProjectSubmitted(projectId, msg.sender, name, url);
    }

    /// @notice 给项目投票
    function voteForProject(uint256 projectId) external payable onlyVotePhase {
        if (projectId >= projectCount)
            revert InvalidProjectId(projectId);
        if (msg.value == 0)
            revert InvalidAmount();

        projects[projectId].totalRaised += msg.value;
        emit ProjectVoted(msg.sender, projectId, msg.value);
    }

    function closeRound() external onlyAfterCampaign {
        if (settled) revert AlreadySettled();

        uint256 winnerId;
        uint256 maxRaised;
        uint256 count = projectCount;
        for (uint256 i = 0; i < count; ++i) {
            uint256 raised = projects[i].totalRaised;
            if (raised > maxRaised) {
                winnerId = i;
                maxRaised = raised;
            }
        }

        settled = true;
        if (maxRaised == 0) {
            emit RoundClosedWithNoWinner();
            return;
        }

        winningProject = winnerId;
        uint256 prize = address(this).balance;
        address winner = projects[winnerId].submitter;
        emit WinnerAnnounced(winningProject, winner, prize);

        (bool success, ) = payable(winner).call{value: prize}("");
        if (!success) revert PayoutFailed();
    }
}